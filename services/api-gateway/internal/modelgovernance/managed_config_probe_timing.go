package modelgovernance

import (
	"crypto/tls"
	"net/http/httptrace"
	"sync"
	"time"
)

type managedRequestTimingKey struct{}

type managedRequestTiming struct {
	mu         sync.Mutex
	started    time.Time
	dnsStarted time.Time
	connectAt  time.Time
	tlsStarted time.Time
	stage      string
	diagnostic ManagedAPIConnectionDiagnostic
}

func newManagedRequestTiming() *managedRequestTiming {
	return &managedRequestTiming{started: time.Now(), stage: "response"}
}

func (timing *managedRequestTiming) clientTrace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		TLSHandshakeStart: func() {
			timing.mu.Lock()
			timing.tlsStarted = time.Now()
			timing.stage = "tls"
			timing.mu.Unlock()
		},
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			timing.mu.Lock()
			if !timing.tlsStarted.IsZero() {
				timing.diagnostic.TLSMS += time.Since(timing.tlsStarted).Milliseconds()
			}
			if err == nil {
				timing.stage = "response"
			}
			timing.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			timing.mu.Lock()
			timing.diagnostic.TTFBMS = time.Since(timing.started).Milliseconds()
			timing.stage = "body"
			timing.mu.Unlock()
		},
	}
}

func (timing *managedRequestTiming) beginDNS() {
	if timing == nil {
		return
	}
	timing.mu.Lock()
	timing.dnsStarted = time.Now()
	timing.stage = "dns"
	timing.mu.Unlock()
}

func (timing *managedRequestTiming) endDNS(err error) {
	if timing == nil {
		return
	}
	timing.mu.Lock()
	timing.diagnostic.DNSMS += time.Since(timing.dnsStarted).Milliseconds()
	if err == nil {
		timing.stage = "connect"
	}
	timing.mu.Unlock()
}

func (timing *managedRequestTiming) beginConnect() {
	if timing == nil {
		return
	}
	timing.mu.Lock()
	timing.connectAt = time.Now()
	timing.stage = "connect"
	timing.mu.Unlock()
}

func (timing *managedRequestTiming) endConnect(err error) {
	if timing == nil {
		return
	}
	timing.mu.Lock()
	timing.diagnostic.ConnectMS += time.Since(timing.connectAt).Milliseconds()
	if err == nil {
		timing.stage = "tls"
	}
	timing.mu.Unlock()
}

func (timing *managedRequestTiming) snapshot() (ManagedAPIConnectionDiagnostic, string) {
	timing.mu.Lock()
	defer timing.mu.Unlock()
	return timing.diagnostic, timing.stage
}
