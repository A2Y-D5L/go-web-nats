package platform

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

func startEmbeddedNATS() (*server.Server, string, string, bool, error) {
	storeCfg := resolveNATSStoreDir()
	storeDir := storeCfg.storeDir
	var err error
	if storeCfg.isEphemeral {
		storeDir, err = os.MkdirTemp("", "nats-js-*")
		if err != nil {
			return nil, "", "", false, err
		}
	} else {
		err = os.MkdirAll(storeDir, dirModePrivateRead)
		if err != nil {
			return nil, "", "", false, err
		}
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
		if storeCfg.isEphemeral {
			_ = os.RemoveAll(storeDir)
		}
		return nil, "", "", false, err
	}
	ns.ConfigureLogger()
	ns.Start()
	if !ns.ReadyForConnections(defaultStartupWait) {
		ns.Shutdown()
		ns.WaitForShutdown()
		if storeCfg.isEphemeral {
			_ = os.RemoveAll(storeDir)
		}
		return nil, "", "", false, errors.New("nats not ready")
	}
	return ns, ns.ClientURL(), storeDir, storeCfg.isEphemeral, nil
}

func ensureWorkerDeliveryStream(ctx context.Context, js jetstream.JetStream) error {
	var cfg jetstream.StreamConfig
	cfg.Name = streamWorkerPipeline
	cfg.Subjects = []string{
		subjectProjectOpStart,
		subjectRegistrationDone,
		subjectBootstrapDone,
		subjectBuildDone,
		subjectDeployDone,
		subjectDeploymentStart,
		subjectDeploymentDone,
		subjectPromotionStart,
		subjectPromotionDone,
		subjectWorkerPoison,
	}
	cfg.Retention = jetstream.LimitsPolicy
	cfg.MaxMsgs = workerDeliveryStreamMaxMsgs
	cfg.MaxBytes = workerDeliveryStreamMaxBytes
	cfg.Discard = jetstream.DiscardOld
	cfg.MaxAge = workerDeliveryStreamMaxAge
	cfg.Storage = jetstream.FileStorage
	cfg.Replicas = 1
	_, err := js.CreateOrUpdateStream(ctx, cfg)
	return err
}
