package useractivity

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protowire"
)

const Stream = "stream:{user_activity}"

type Event struct {
	EventID, UserID, Category, Action, ActorType, ActorID  string
	Outcome, Source, ResourceType, ResourceID, OperationID string
	Title, Summary                                         string
	OccurredAt                                             time.Time
	Metadata                                               map[string]any
}

func Append(ctx context.Context, client *goredis.Client, event Event) error {
	if client == nil || event.EventID == "" || event.UserID == "" || event.Action == "" ||
		event.Source == "" || event.Category == "" || event.Outcome == "" {
		return fmt.Errorf("user activity producer input is invalid")
	}
	metadata, err := json.Marshal(event.Metadata)
	if err != nil || len(metadata) > 16*1024 {
		return fmt.Errorf("user activity metadata is invalid or too large")
	}
	payload := marshal(event, string(metadata))
	_, err = client.Eval(ctx, `
		if redis.call("XLEN", KEYS[1]) >= tonumber(ARGV[1]) then
			return redis.error_reply("USER_ACTIVITY_STREAM_CAPACITY_REACHED")
		end
		return redis.call("XADD", KEYS[1], "*", "payload", ARGV[2])
	`, []string{Stream}, 100000, payload).Result()
	return err
}

func marshal(event Event, metadata string) []byte {
	var out []byte
	out = appendString(out, 1, event.EventID)
	out = appendString(out, 2, event.UserID)
	out = appendString(out, 3, event.Category)
	out = appendString(out, 4, event.Action)
	out = appendString(out, 5, event.ActorType)
	out = appendString(out, 6, event.ActorID)
	out = appendString(out, 7, event.Outcome)
	out = appendString(out, 8, event.Source)
	out = appendString(out, 9, event.ResourceType)
	out = appendString(out, 10, event.ResourceID)
	out = appendString(out, 11, event.OperationID)
	out = appendString(out, 12, event.Title)
	out = appendString(out, 13, event.Summary)
	out = protowire.AppendTag(out, 14, protowire.VarintType)
	out = protowire.AppendVarint(out, uint64(event.OccurredAt.Unix()))
	out = appendString(out, 15, metadata)
	out = protowire.AppendTag(out, 16, protowire.VarintType)
	out = protowire.AppendVarint(out, 1)
	return out
}

func appendString(dst []byte, number protowire.Number, value string) []byte {
	if value == "" {
		return dst
	}
	dst = protowire.AppendTag(dst, number, protowire.BytesType)
	return protowire.AppendString(dst, value)
}
