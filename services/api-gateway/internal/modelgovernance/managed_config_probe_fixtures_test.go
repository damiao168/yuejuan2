package modelgovernance

import (
	"context"
)

func successfulManagedProbe() ManagedAPIProbeResult {
	return ManagedAPIProbeResult{
		OK: true, StatusCode: 200, LatencyMS: 18, Message: "配置验证成功",
		CredentialCheck: ManagedAPICheckResult{OK: true}, ModelCheck: ManagedAPICheckResult{OK: true},
		TextCheck:       ManagedAPICheckResult{OK: true},
		CapabilityCheck: ManagedAPICheckResult{OK: true},
	}
}

type countingManagedProber struct {
	calls  *int
	result ManagedAPIProbeResult
}

type recordingManagedProber struct {
	seen    *[]ManagedAPIConnection
	onQuick func()
}

func (p recordingManagedProber) ProbeQuick(_ context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	*p.seen = append(*p.seen, connection)
	if p.onQuick != nil {
		p.onQuick()
	}
	return successfulManagedProbe()
}

func (p recordingManagedProber) Probe(_ context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	*p.seen = append(*p.seen, connection)
	return successfulManagedProbe()
}

func (p recordingManagedProber) ProbeText(ctx context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	return managedTextProbeFixture(p.Probe(ctx, connection))
}

type generatedOnlyManagedProber struct {
	calls *int
}

func (p generatedOnlyManagedProber) Probe(_ context.Context, _ ManagedAPIConnection) ManagedAPIProbeResult {
	*p.calls++
	return successfulManagedProbe()
}

func (p countingManagedProber) Probe(_ context.Context, _ ManagedAPIConnection) ManagedAPIProbeResult {
	*p.calls++
	return p.result
}

func (p countingManagedProber) ProbeQuick(_ context.Context, _ ManagedAPIConnection) ManagedAPIProbeResult {
	*p.calls++
	result := p.result
	result.ProbeMode = "quick"
	result.GeneratedRequest = false
	return result
}

func (p countingManagedProber) ProbeText(ctx context.Context, connection ManagedAPIConnection) ManagedAPIProbeResult {
	return managedTextProbeFixture(p.Probe(ctx, connection))
}

type listingManagedProber struct {
	models       []string
	providerSeen *string
	keySeen      *string
}

type failingListingManagedProber struct{}

func (failingListingManagedProber) Probe(_ context.Context, _ ManagedAPIConnection) ManagedAPIProbeResult {
	return successfulManagedProbe()
}

func (failingListingManagedProber) ListModels(_ context.Context, connection ManagedAPIConnection) (ManagedAPIModelListResult, ManagedAPIProbeResult) {
	return ManagedAPIModelListResult{}, ManagedAPIProbeResult{
		Provider: connection.Config.ProviderKey, ProbeMode: "quick", ErrorCode: "provider_timeout",
		Message: "upstream response body", ConnectionDiagnostic: ManagedAPIConnectionDiagnostic{
			Attempts: 2, DNSMS: 3, ConnectMS: 6, TLSMS: 0, TTFBMS: 0, FailureStage: "connect",
		},
		Diagnostic: ManagedAPIProbeDiagnostic{ContentPreview: "upstream response body", ContentSHA256: "203.0.113.9"},
	}
}

func (p listingManagedProber) Probe(_ context.Context, _ ManagedAPIConnection) ManagedAPIProbeResult {
	return successfulManagedProbe()
}

func (p listingManagedProber) ListModels(_ context.Context, connection ManagedAPIConnection) (ManagedAPIModelListResult, ManagedAPIProbeResult) {
	provider := connection.Config.ProviderKey
	if p.providerSeen != nil {
		*p.providerSeen = provider
	}
	if p.keySeen != nil {
		*p.keySeen = connection.APIKey
	}
	return ManagedAPIModelListResult{Provider: provider, Models: p.models}, successfulManagedProbe()
}
