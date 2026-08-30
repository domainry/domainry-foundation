package worker

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"math/big"
)

type FaultPoint string

const (
	FaultTransactionBeforeBegin  FaultPoint = "transaction.before_begin"
	FaultTransactionAfterBegin   FaultPoint = "transaction.after_begin"
	FaultTransactionBeforeWrite  FaultPoint = "transaction.before_write"
	FaultTransactionAfterWrite   FaultPoint = "transaction.after_write"
	FaultTransactionBeforeCommit FaultPoint = "transaction.before_commit"
	FaultTransactionAfterCommit  FaultPoint = "transaction.after_commit"
	FaultWorkerClaim             FaultPoint = "worker.claim"
	FaultWorkerHeartbeat         FaultPoint = "worker.heartbeat"
	FaultProviderBeforeSend      FaultPoint = "provider.before_send"
	FaultProviderAfterSend       FaultPoint = "provider.after_send"
	FaultWorkerBeforeComplete    FaultPoint = "worker.before_complete"
	FaultWorkerAfterComplete     FaultPoint = "worker.after_complete"
)

type FaultInjector interface {
	Check(context.Context, FaultPoint) error
}
type NoopFaultInjector struct{}

func (NoopFaultInjector) Check(context.Context, FaultPoint) error { return nil }

type IdentifierGenerator interface{ NewID() string }
type CryptoIdentifierGenerator struct{}

func (CryptoIdentifierGenerator) NewID() string {
	value := make([]byte, 16)
	_, _ = cryptorand.Read(value)
	return hex.EncodeToString(value)
}

type RandomSource interface{ Int63n(int64) int64 }
type CryptoRandomSource struct{}

func (CryptoRandomSource) Int63n(max int64) int64 {
	if max <= 0 {
		return 0
	}
	value, err := cryptorand.Int(cryptorand.Reader, big.NewInt(max))
	if err != nil {
		return 0
	}
	return value.Int64()
}

func CheckFault(ctx context.Context, injector FaultInjector, point FaultPoint) error {
	if injector == nil {
		return nil
	}
	return injector.Check(ctx, point)
}
