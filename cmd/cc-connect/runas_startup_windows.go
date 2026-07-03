//go:build windows

package main

import (
	"context"

	"github.com/YingSuiAI/dirextalk-connect/config"
)

func runRunAsUserStartupChecks(_ context.Context, _ *config.Config) error {
	return nil
}
