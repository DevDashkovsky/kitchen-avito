package worker

import (
	"context"
	"log/slog"
	"time"
)

type confirmationTimeoutService interface {
	RejectExpired(context.Context, time.Time) (int64, error)
}

type ConfirmationTimeout struct {
	service  confirmationTimeoutService
	timeout  time.Duration
	interval time.Duration
	logger   *slog.Logger
}

func NewConfirmationTimeout(
	service confirmationTimeoutService,
	timeout time.Duration,
	interval time.Duration,
	logger *slog.Logger,
) *ConfirmationTimeout {
	return &ConfirmationTimeout{
		service:  service,
		timeout:  timeout,
		interval: interval,
		logger:   logger,
	}
}

func (w *ConfirmationTimeout) Run(ctx context.Context) {
	w.process(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.process(ctx)
		}
	}
}

func (w *ConfirmationTimeout) process(ctx context.Context) {
	createdBefore := time.Now().Add(-w.timeout)
	rejected, err := w.service.RejectExpired(ctx, createdBefore)
	if err != nil {
		if ctx.Err() == nil {
			w.logger.Error("reject expired orders", "error", err)
		}
		return
	}

	if rejected > 0 {
		w.logger.Info("expired orders rejected", "count", rejected)
	}
}
