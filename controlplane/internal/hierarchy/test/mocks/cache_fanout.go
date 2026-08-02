package mocks

import "context"

type CacheFanout struct {
	Calls   int
	Key     string
	Payload []byte
	Err     error
}

func (f *CacheFanout) Publish(_ context.Context, key string, payload []byte) (int64, error) {
	f.Calls++
	f.Key = key
	f.Payload = payload
	return int64(f.Calls), f.Err
}
