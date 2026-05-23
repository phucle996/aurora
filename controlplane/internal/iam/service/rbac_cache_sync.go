package iamSvcImpl

import (
	"context"
	"encoding/json"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"controlplane/pkg/logger"

	iamCache "controlplane/internal/iam/cache"
	goredis "github.com/redis/go-redis/v9"
)

const defaultRbacSyncInterval = 30 * time.Second

type RbacCacheSync struct {
	store    iamCache.RbacSyncStore
	registry *RoleRegistry
	interval time.Duration

	cancel  context.CancelFunc
	done    chan struct{}
	once    sync.Once
	started atomic.Bool
	epoch   int64
}

func NewRbacCacheSync(store iamCache.RbacSyncStore, registry *RoleRegistry) *RbacCacheSync {
	if store == nil || registry == nil {
		return nil
	}
	return &RbacCacheSync{store: store, registry: registry, interval: defaultRbacSyncInterval, done: make(chan struct{})}
}

func (s *RbacCacheSync) Start(parent context.Context) {
	if s == nil || s.store == nil || s.registry == nil {
		return
	}
	if !s.started.CompareAndSwap(false, true) {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel

	pubsub := s.store.Subscribe(ctx)
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		s.once.Do(func() {
			if s.cancel != nil {
				s.cancel()
			}
		})
		s.started.Store(false)
		logger.SysWarn("iam.rbac.sync", "subscribe failed: "+err.Error())
		return
	}
	if initialEpoch, err := s.store.LoadEpoch(ctx); err == nil {
		atomic.StoreInt64(&s.epoch, initialEpoch)
	}
	go s.loop(ctx, pubsub)
}

func (s *RbacCacheSync) Stop() {
	if s == nil || !s.started.Load() {
		return
	}
	s.once.Do(func() {
		if s.cancel != nil {
			s.cancel()
		}
	})
	<-s.done
}

func (s *RbacCacheSync) loop(ctx context.Context, pubsub *goredis.PubSub) {
	defer close(s.done)
	defer func() { _ = pubsub.Close() }()
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	jitter := time.Duration(rng.Intn(2000)) * time.Millisecond
	ticker := time.NewTicker(s.interval + jitter)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-pubsub.Channel():
			if !ok {
				return
			}
			s.handleMessage(msg.Payload)
		case <-ticker.C:
			s.syncEpoch(ctx)
		}
	}
}

func (s *RbacCacheSync) handleMessage(payload string) {
	var event iamCache.RbacInvalidateEvent
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		return
	}
	current := atomic.LoadInt64(&s.epoch)
	if event.Epoch > current {
		atomic.StoreInt64(&s.epoch, event.Epoch)
	}
	switch event.Kind {
	case iamCache.RbacInvalidateAll:
		s.registry.InvalidateAll()
	case iamCache.RbacInvalidateRole:
		if event.Role != "" {
			s.registry.Invalidate(event.Role)
		}
	}
}

func (s *RbacCacheSync) syncEpoch(ctx context.Context) {
	current, err := s.store.LoadEpoch(ctx)
	if err != nil {
		logger.SysWarn("iam.rbac.sync", "load epoch failed: "+err.Error())
		return
	}
	last := atomic.LoadInt64(&s.epoch)
	if current <= last {
		return
	}
	s.registry.InvalidateAll()
	atomic.StoreInt64(&s.epoch, current)
}
