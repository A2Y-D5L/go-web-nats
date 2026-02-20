package main

import (
	"context"
	"errors"
	"os"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go/jetstream"
)

////////////////////////////////////////////////////////////////////////////////
// Infrastructure: Embedded NATS + JetStream KV
////////////////////////////////////////////////////////////////////////////////

func ensureKVBucket(
	ctx context.Context,
	js jetstream.JetStream,
	bucket string,
	history uint8,
	out *jetstream.KeyValue,
) error {
	var cfg jetstream.KeyValueConfig
	cfg.Bucket = bucket
	cfg.History = history

	createdKV, err := js.CreateKeyValue(ctx, cfg)
	if err != nil {
		if errors.Is(err, jetstream.ErrBucketExists) {
			existingKV, getErr := js.KeyValue(ctx, bucket)
			if getErr != nil {
				return getErr
			}
			*out = existingKV
			return nil
		}
		return err
	}
	*out = createdKV
	return nil
}

func startEmbeddedNATS() (*server.Server, string, string, error) {
	storeDir, err := os.MkdirTemp("", "nats-js-*")
	if err != nil {
		return nil, "", "", err
	}
	var opts server.Options
	opts.ServerName = "embedded-paas"
	opts.Host = "127.0.0.1"
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = storeDir
	opts.NoSigs = true

	ns, err := server.NewServer(&opts)
	if err != nil {
		_ = os.RemoveAll(storeDir)
		return nil, "", "", err
	}
	ns.ConfigureLogger()
	ns.Start()
	if !ns.ReadyForConnections(defaultStartupWait) {
		ns.Shutdown()
		ns.WaitForShutdown()
		_ = os.RemoveAll(storeDir)
		return nil, "", "", errors.New("nats not ready")
	}
	return ns, ns.ClientURL(), storeDir, nil
}
