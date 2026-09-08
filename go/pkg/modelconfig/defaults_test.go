package modelconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeCatalogs_ReplaceByID(t *testing.T) {
	base, err := NewCatalog([]ModelCatalogEntry{
		{ID: "m1", Aliases: []string{"a1"}, Tier: TierJudgment, Provider: "old-provider"},
		{ID: "m2", Aliases: []string{"a2"}, Tier: TierExecution},
	})
	require.NoError(t, err)

	user, err := NewCatalog([]ModelCatalogEntry{
		{ID: "m1", Aliases: []string{"a1"}, Tier: TierJudgment, Provider: "new-provider"},
	})
	require.NoError(t, err)

	merged, err := MergeCatalogs(base, user)
	require.NoError(t, err)

	// m1 should be replaced with user version.
	entry, ok := merged.Lookup("m1")
	require.True(t, ok)
	assert.Equal(t, "new-provider", entry.Provider)

	// m2 should be kept from base.
	entry, ok = merged.Lookup("m2")
	require.True(t, ok)
	assert.Equal(t, TierExecution, entry.Tier)

	assert.Len(t, merged.All(), 2)
}

func TestMergeCatalogs_AppendNew(t *testing.T) {
	base, err := NewCatalog([]ModelCatalogEntry{
		{ID: "m1", Aliases: []string{"a1"}},
	})
	require.NoError(t, err)

	user, err := NewCatalog([]ModelCatalogEntry{
		{ID: "m2", Aliases: []string{"a2"}},
	})
	require.NoError(t, err)

	merged, err := MergeCatalogs(base, user)
	require.NoError(t, err)
	assert.Len(t, merged.All(), 2)

	_, ok := merged.Lookup("m1")
	assert.True(t, ok)
	_, ok = merged.Lookup("m2")
	assert.True(t, ok)
}

func TestMergeCatalogs_AliasConflictError(t *testing.T) {
	base, err := NewCatalog([]ModelCatalogEntry{
		{ID: "m1", Aliases: []string{"shared"}},
	})
	require.NoError(t, err)

	user, err := NewCatalog([]ModelCatalogEntry{
		{ID: "m2", Aliases: []string{"shared"}},
	})
	require.NoError(t, err)

	_, err = MergeCatalogs(base, user)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate alias")
}

func TestMergeCatalogs_EmptyUser(t *testing.T) {
	base, err := NewCatalog(testEntries())
	require.NoError(t, err)

	user, err := NewCatalog(nil)
	require.NoError(t, err)

	merged, err := MergeCatalogs(base, user)
	require.NoError(t, err)
	assert.Len(t, merged.All(), len(base.All()))
}

func TestResolverFromCatalogFile(t *testing.T) {
	// Write a custom catalog that adds an ollama model and overrides a role default.
	yaml := `
models:
  - id: ollama-llama3
    aliases: [llama3]
    provider: ollama
    auth_mode: local
    tier: volume
    capabilities: [coding, fast-latency]
    context_window: 8192
    max_output_tokens: 4096
    pricing:
      input_per_million: 0
      output_per_million: 0

role_defaults:
  guardian: llama3

profiles:
  local-fast:
    model: llama3
    provider: ollama
`
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))

	resolver, err := ResolverFromCatalogFile(path)
	require.NoError(t, err)

	// The new model should be resolvable.
	rc, err := resolver.Resolve(ResolutionInput{
		Role:          "worker",
		AgentDefModel: "llama3",
	})
	require.NoError(t, err)
	assert.Equal(t, "ollama-llama3", rc.Model)
	assert.Equal(t, "ollama", rc.Entry.Provider)
	assert.Equal(t, AuthLocal, rc.AuthMode)

	// Guardian role default should now resolve to llama3.
	rc, err = resolver.Resolve(ResolutionInput{Role: "guardian"})
	require.NoError(t, err)
	assert.Equal(t, "ollama-llama3", rc.Model)

	// Built-in models should still be available.
	rc, err = resolver.Resolve(ResolutionInput{
		Role:          "worker",
		AgentDefModel: "opus",
	})
	require.NoError(t, err)
	assert.Equal(t, "claude-opus-4-6", rc.Model)

	// New profile should work.
	rc, err = resolver.Resolve(ResolutionInput{
		Role:   "worker",
		Policy: &RoleModelPolicy{Profile: "local-fast"},
	})
	require.NoError(t, err)
	assert.Equal(t, "ollama-llama3", rc.Model)

	// Built-in profiles should still work.
	rc, err = resolver.Resolve(ResolutionInput{
		Role:   "worker",
		Policy: &RoleModelPolicy{Profile: "balanced"},
	})
	require.NoError(t, err)
	assert.Equal(t, "claude-sonnet-4-6", rc.Model)
}

func TestDefaultResolverIncludesCodexOperateProfile(t *testing.T) {
	rc, err := DefaultResolver().Resolve(ResolutionInput{
		Role:       "implementer",
		Policy:     &RoleModelPolicy{Profile: "codex-operate"},
		CanOperate: true,
	})
	require.NoError(t, err)
	assert.Equal(t, "gpt-5.5", rc.Model)
	assert.Equal(t, "codex-cli", rc.Provider)
	assert.Equal(t, AuthSubscription, rc.AuthMode)
	assert.Empty(t, ValidateCapabilities(rc.Entry, []Capability{CapCoding, CapOperate}))
}

// opusCapabilitySet is the D1 capability set shared by claude-opus-4-6,
// claude-opus-4-8, and claude-fable-5.
var opusCapabilitySet = []Capability{
	CapTools, CapReasoning, CapCoding, CapVision, CapOperate, CapLargeContext, CapStructuredOut,
}

func TestDefaultCatalog_NewModelEntries(t *testing.T) {
	cat := DefaultCatalog()

	tests := []struct {
		name        string
		lookups     []string // canonical ID plus every alias — all must resolve
		wantID      string
		wantPricing ModelPricing
	}{
		{
			name:    "claude-opus-4-8",
			lookups: []string{"claude-opus-4-8", "opus-4-8"},
			wantID:  "claude-opus-4-8",
			wantPricing: ModelPricing{
				InputPerMillion:      5.00,
				OutputPerMillion:     25.00,
				CacheReadPerMillion:  0.50,
				CacheWritePerMillion: 6.25,
			},
		},
		{
			name:    "claude-fable-5",
			lookups: []string{"claude-fable-5", "fable", "fable-5"},
			wantID:  "claude-fable-5",
			wantPricing: ModelPricing{
				InputPerMillion:      10.00,
				OutputPerMillion:     50.00,
				CacheReadPerMillion:  1.00,
				CacheWritePerMillion: 12.50,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, q := range tt.lookups {
				entry, ok := cat.Lookup(q)
				require.True(t, ok, "Lookup(%q) must resolve", q)
				assert.Equal(t, tt.wantID, entry.ID)
				assert.Equal(t, "claude-cli", entry.Provider)
				assert.Equal(t, AuthSubscription, entry.AuthMode)
				assert.Equal(t, TierJudgment, entry.Tier)
				assert.Equal(t, opusCapabilitySet, entry.Capabilities)
				assert.Equal(t, 1_000_000, entry.ContextWindow)
				assert.Equal(t, 16384, entry.MaxOutputTokens)
				assert.Equal(t, tt.wantPricing, entry.Pricing)
			}
		})
	}
}

func TestDefaultCatalog_OpusAliasUnchanged(t *testing.T) {
	// IADA-4: no silent rebinding of a live alias — "opus" stays on 4-6.
	entry, ok := DefaultCatalog().Lookup("opus")
	require.True(t, ok)
	assert.Equal(t, "claude-opus-4-6", entry.ID)
}

func TestDefaultCatalog_OpusPricingCorrected(t *testing.T) {
	// D1a: published Opus 4.6 pricing is $5/$25 (cache $0.50/$6.25) —
	// the old 15/75/1.50/18.75 entry was stale. Both the claude-cli entry
	// and its api- variant carry pricing, so both are corrected.
	want := ModelPricing{
		InputPerMillion:      5.00,
		OutputPerMillion:     25.00,
		CacheReadPerMillion:  0.50,
		CacheWritePerMillion: 6.25,
	}
	for _, id := range []string{"claude-opus-4-6", "api-claude-opus-4-6"} {
		entry, ok := DefaultCatalog().Lookup(id)
		require.True(t, ok, id)
		assert.Equal(t, want, entry.Pricing, id)
	}
	// IADA-3: context_window intentionally NOT bumped — pricing was the
	// flagged dishonesty; capability semantics for an in-use model are a
	// separate decision.
	entry, ok := DefaultCatalog().Lookup("claude-opus-4-6")
	require.True(t, ok)
	assert.Equal(t, 200_000, entry.ContextWindow)
}

func TestDefaultCatalog_TierCheapnessOrderUnchanged(t *testing.T) {
	// IADA-2: the correction must not reorder tier cheapness —
	// haiku < sonnet < opus-4-6 < fable by output price.
	order := []string{"claude-haiku-4-5-20251001", "claude-sonnet-4-6", "claude-opus-4-6", "claude-fable-5"}
	prev := -1.0
	for _, id := range order {
		entry, ok := DefaultCatalog().Lookup(id)
		require.True(t, ok, id)
		assert.Greater(t, entry.Pricing.OutputPerMillion, prev,
			"%s must be strictly more expensive than its predecessor", id)
		prev = entry.Pricing.OutputPerMillion
	}
}

func TestDefaultCatalog_CheapestWithCapabilities_OpusTie(t *testing.T) {
	// CFADA1-adv1: claude-opus-4-6 and claude-opus-4-8 TIE at output 25.00.
	// CheapestWithCapabilities uses strict < (catalog.go), so on a tie the
	// FIRST entry in catalog order wins. In defaults_catalog.yaml,
	// claude-opus-4-6 precedes claude-opus-4-8, so 4-6 wins the tie.
	cat := DefaultCatalog()
	opus46, ok := cat.Lookup("claude-opus-4-6")
	require.True(t, ok)
	opus48, ok := cat.Lookup("claude-opus-4-8")
	require.True(t, ok)
	require.Equal(t, opus46.Pricing.OutputPerMillion, opus48.Pricing.OutputPerMillion,
		"the tie this test documents: both at output 25.00")

	// Restrict to the two tied entries, preserving default-catalog order,
	// so the tie is the only thing being decided.
	var tied []ModelCatalogEntry
	for _, e := range cat.All() {
		if e.ID == "claude-opus-4-6" || e.ID == "claude-opus-4-8" {
			tied = append(tied, e)
		}
	}
	require.Len(t, tied, 2)
	require.Equal(t, "claude-opus-4-6", tied[0].ID, "4-6 must precede 4-8 in catalog order")

	tiedCat, err := NewCatalog(tied)
	require.NoError(t, err)
	best, found := tiedCat.CheapestWithCapabilities(opusCapabilitySet)
	require.True(t, found)
	assert.Equal(t, "claude-opus-4-6", best.ID,
		"strict < keeps first-in-catalog on a price tie")
}

func TestDefaultCatalog_OpusCorrectionCostGateFlip(t *testing.T) {
	// IADA-2: the D1a correction is behavior-relevant. At the reference call
	// size (10k input + 2k output), old pricing (15/75) estimated
	// $0.30/call; corrected pricing (5/25) estimates $0.10/call. A
	// MaxCostPerCallUSD of $0.20 excluded claude-opus-4-6 before the
	// correction and includes it after.
	costCap := 0.20

	rc, err := DefaultResolver().Resolve(ResolutionInput{
		Role:          "worker",
		AgentDefModel: "claude-opus-4-6",
		Policy:        &RoleModelPolicy{MaxCostPerCallUSD: &costCap},
	})
	require.NoError(t, err, "corrected pricing must pass the $0.20 cost gate")
	assert.Equal(t, "claude-opus-4-6", rc.Model)
	assert.InDelta(t, 0.10, rc.Entry.Pricing.EstimateCost(10_000, 2_000), 1e-9)

	// The pre-correction pricing would have been rejected by the same gate.
	oldPricing := ModelPricing{InputPerMillion: 15.00, OutputPerMillion: 75.00}
	assert.Greater(t, oldPricing.EstimateCost(10_000, 2_000), costCap,
		"old pricing exceeded the gate — the flip is visible, not silent")
}

func TestResolverFromCatalogFile_ModelAliases(t *testing.T) {
	yaml := `
model_aliases:
  sonnet: codex
  claude-haiku-4-5-20251001: codex-fast
`
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))

	resolver, err := ResolverFromCatalogFile(path)
	require.NoError(t, err)

	rc, err := resolver.Resolve(ResolutionInput{
		Role:          "spawned-role",
		AgentDefModel: "sonnet",
	})
	require.NoError(t, err)
	assert.Equal(t, "gpt-5.5", rc.Model)
	assert.Equal(t, "codex-cli", rc.Provider)

	rc, err = resolver.Resolve(ResolutionInput{
		Role:          "spawned-role",
		AgentDefModel: "claude-haiku-4-5-20251001",
	})
	require.NoError(t, err)
	assert.Equal(t, "gpt-5.4", rc.Model)
	assert.Equal(t, "codex-cli", rc.Provider)

	_, err = resolver.Resolve(ResolutionInput{
		Role:          "spawned-role",
		AgentDefModel: "sonnet",
		Policy:        &RoleModelPolicy{Provider: "claude-cli"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "belongs to provider")
}

func TestResolverFromCatalogFile_ModelAliasUnknownTarget(t *testing.T) {
	yaml := `
model_aliases:
  sonnet: does-not-exist
`
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.yaml")
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))

	_, err := ResolverFromCatalogFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model_aliases target")
}

func TestResolverFromCatalogFile_BadPath(t *testing.T) {
	_, err := ResolverFromCatalogFile("/nonexistent/catalog.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read catalog file")
}

func TestResolverFromCatalogFile_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	require.NoError(t, os.WriteFile(path, []byte("models: [[[invalid"), 0o644))

	_, err := ResolverFromCatalogFile(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse catalog file")
}

func TestCurrentProviderModelsResolveWithoutChangingPins(t *testing.T) {
	for _, id := range []string{"gpt-6-astra", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "claude-fable-5-1", "claude-opus-5", "claude-sonnet-5"} {
		rc, err := DefaultResolver().Resolve(ResolutionInput{AgentDefModel: id, CanOperate: true})
		require.NoError(t, err, id)
		assert.Equal(t, id, rc.Model)
		assert.Equal(t, "2026-09-08", rc.Entry.Metadata["verified_at"])
		assert.Equal(t, 128000, rc.Entry.MaxOutputTokens)
		assert.GreaterOrEqual(t, rc.Entry.ContextWindow, 1000000)
	}
	for _, id := range []string{"api-claude-fable-5-1", "api-claude-opus-5", "api-claude-sonnet-5"} {
		rc, err := DefaultResolver().Resolve(ResolutionInput{AgentDefModel: id})
		require.NoError(t, err)
		assert.Equal(t, "anthropic", rc.Provider)
		_, err = DefaultResolver().Resolve(ResolutionInput{AgentDefModel: id, CanOperate: true})
		assert.Error(t, err, "API model must not gain CLI operate capability")
	}
	rc, err := DefaultResolver().Resolve(ResolutionInput{AgentDefModel: "gpt-5.5"})
	require.NoError(t, err)
	assert.Equal(t, "gpt-5.5", rc.Model)
	fable, _ := DefaultCatalog().Lookup("claude-fable-5-1")
	assert.Equal(t, 0.25, fable.Pricing.CacheReadPerMillion)
	haiku, _ := DefaultCatalog().Lookup("claude-haiku-4-5-20251001")
	assert.Equal(t, 5.0, haiku.Pricing.OutputPerMillion)
}
