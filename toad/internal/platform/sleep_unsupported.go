//go:build !linux && !darwin

package platform

import (
	"context"
	"errors"
)

var ErrSleepUnsupported = errors.New("sleep notifications are unsupported on this platform")

type unsupportedSleepSource struct{}

func DefaultSleepSource() SleepSource { return unsupportedSleepSource{} }

func (unsupportedSleepSource) Watch(context.Context, chan<- SleepEvent) error {
	return ErrSleepUnsupported
}
