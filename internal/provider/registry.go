package provider

import (
	"errors"
	"fmt"
	"strings"

	"github.com/giovannialves/corvex/internal/config"
	"github.com/giovannialves/corvex/internal/provider/claude"
)

var ErrUnknownProvider = errors.New("unknown provider")

func NewProvider(name string, cfg *config.Config) (Provider, error) {
	normalized := strings.TrimSpace(strings.ToLower(name))

	switch normalized {
	case "claude", "claude-cli":
		return claude.New(cfg), nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownProvider, name)
	}
}
