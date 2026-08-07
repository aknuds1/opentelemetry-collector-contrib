// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package internal // import "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal"

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/otel/sdk/resource"
	conventions "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.uber.org/zap"

	"github.com/open-telemetry/opentelemetry-collector-contrib/receiver/hostmetricsreceiver/internal/metadata"
)

// otelNamespaceUUID is the namespace the semantic conventions prescribe for deterministically
// generated service.instance.id values.
// See https://opentelemetry.io/docs/specs/semconv/registry/attributes/service/.
var otelNamespaceUUID = uuid.MustParse("4d63009a-8d0f-11ee-aad7-4c796ed8e320")

// hostIDDetectionTimeout bounds the host ID probe. The OTel SDK's darwin and BSD readers shell out
// to a subprocess built with context.Background(), so the caller's context cannot cancel them;
// without a deadline of our own a wedged process would block startup indefinitely.
const hostIDDetectionTimeout = 5 * time.Second

// HostIdentity identifies the host being scraped. Either field is empty when the platform exposes
// no such identifier.
type HostIdentity struct {
	ID   string
	Name string
}

// HostIdentitySource resolves the host identity at most once and shares the result between
// scrapers, so a receiver probes the platform once rather than once per scraper.
type HostIdentitySource struct {
	logger *zap.Logger
	// detect is a seam for tests, which need to count probes to prove they are shared.
	detect   func(context.Context, *zap.Logger) HostIdentity
	once     sync.Once
	identity HostIdentity
}

func NewHostIdentitySource(logger *zap.Logger) *HostIdentitySource {
	return &HostIdentitySource{logger: logger, detect: detectHostIdentity}
}

// Get resolves the identity on first call and returns that same value thereafter. It never fails:
// fields it cannot determine are left empty, and callers decide what to omit as a result.
//
// The result is cached whether or not detection succeeded, so a first call whose context is already
// cancelled leaves this receiver without host identity for its lifetime. That is tolerable because
// the underlying sources — the hostname syscall and the platform's machine ID — are stable, and
// retrying per scrape would mean re-probing forever on platforms that simply have no host ID.
func (s *HostIdentitySource) Get(ctx context.Context) HostIdentity {
	s.once.Do(func() { s.identity = s.detect(ctx, s.logger) })
	return s.identity
}

func detectHostIdentity(ctx context.Context, logger *zap.Logger) HostIdentity {
	var identity HostIdentity

	if name, err := os.Hostname(); err != nil {
		logger.Debug("Failed to determine host name", zap.Error(err))
	} else {
		identity.Name = name
	}

	// Platforms without a machine-wide identifier (AIX, Solaris) never report one, so a failure
	// here is expected rather than exceptional.
	if id, err := detectHostID(ctx); err != nil {
		logger.Debug("Failed to determine host ID", zap.Error(err))
	} else {
		identity.ID = id
	}

	if identity == (HostIdentity{}) {
		logger.Warn("Failed to determine any host identity, so host.id, host.name and service.instance.id will not be set")
	}

	return identity
}

// detectHostID reads the machine ID on Linux, the IOPlatformUUID on macOS and the MachineGuid on
// Windows, via the OTel SDK's host ID detector, giving up after hostIDDetectionTimeout.
func detectHostID(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, hostIDDetectionTimeout)
	defer cancel()

	type result struct {
		id  string
		err error
	}
	// Buffered, so the goroutine always completes its send and exits even once we have given up
	// waiting for it.
	done := make(chan result, 1)
	go func() {
		id, err := readHostID(ctx)
		done <- result{id: id, err: err}
	}()

	select {
	case r := <-done:
		return r.id, r.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func readHostID(ctx context.Context) (string, error) {
	res, err := resource.New(ctx, resource.WithHostID())
	if err != nil {
		return "", err
	}
	for iter := res.Iter(); iter.Next(); {
		if iter.Attribute().Key == conventions.HostIDKey {
			if id := iter.Attribute().Value.String(); id != "" {
				return id, nil
			}
		}
	}
	return "", errors.New("no host ID reported by the platform")
}

// ServiceInstanceID derives a deterministic UUID v5 from the strongest identifier available: the
// host ID, else the host name. It returns an empty string when neither is known, since seeding
// from a placeholder would collapse every affected host onto a single identity.
func (h HostIdentity) ServiceInstanceID() string {
	seed := h.ID
	if seed == "" {
		seed = h.Name
	}
	if seed == "" {
		return ""
	}
	return uuid.NewSHA1(otelNamespaceUUID, []byte(seed)).String()
}

// ResourceAttributes renders the identity as the subset of resource attributes enabled in cfg.
// withServiceInstanceID is false for scrapers whose resources cannot be told apart by a
// host-derived ID; see anyResourceAttributeEnabled for the matching emptiness check.
func (h HostIdentity) ResourceAttributes(cfg metadata.ResourceAttributesConfig, withServiceInstanceID bool) pcommon.Map {
	rb := metadata.NewResourceBuilder(cfg)
	if h.ID != "" {
		rb.SetHostID(h.ID)
	}
	if h.Name != "" {
		rb.SetHostName(h.Name)
	}
	if withServiceInstanceID {
		if id := h.ServiceInstanceID(); id != "" {
			rb.SetServiceInstanceID(id)
		}
	}
	return rb.Emit().Attributes()
}

// anyResourceAttributeEnabled reports whether any attribute ResourceAttributes could produce is
// enabled. Unlike inspecting the rendered map this is answerable without detecting anything, which
// lets a scraper that would gain nothing skip the wrapper entirely.
func anyResourceAttributeEnabled(cfg metadata.ResourceAttributesConfig, withServiceInstanceID bool) bool {
	return cfg.HostID.Enabled || cfg.HostName.Enabled || (withServiceInstanceID && cfg.ServiceInstanceID.Enabled)
}
