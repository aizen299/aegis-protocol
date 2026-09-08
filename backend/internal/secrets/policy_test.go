package secrets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const devKey = "0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"

type stubProvider struct {
	name   string
	value  string
	err    error
	called int
}

func (p *stubProvider) Name() string { return p.name }

func (p *stubProvider) Fetch(context.Context, string) (Secret, error) {
	p.called++
	if p.err != nil {
		return Secret{}, p.err
	}
	return New(p.value), nil
}

func allProviders(env, ssm *stubProvider) map[string]Provider {
	return map[string]Provider{ProviderEnv: env, ProviderSSM: ssm}
}

// --- the property the policy exists for ---

// A staging process must not reach the development source, even when both are registered and the
// development one would succeed. This is the test that would fail if someone added a fallback.
func TestStagingCannotFallBackToTheDevelopmentSource(t *testing.T) {
	env := &stubProvider{name: ProviderEnv, value: devKey}
	ssm := &stubProvider{name: ProviderSSM, err: errors.New("parameter not found")}

	_, provider, err := Resolve(context.Background(),
		Config{Environment: EnvStaging, Ref: "/aegis/staging/slasher_key"},
		allProviders(env, ssm), nil)

	if err == nil {
		t.Fatal("staging resolved a secret despite SSM failing; it must not fall back")
	}
	if env.called != 0 {
		t.Fatalf("the development source was consulted %d times in staging", env.called)
	}
	if provider != ProviderSSM {
		t.Errorf("reported provider %q, want %q so the failure names the right source", provider, ProviderSSM)
	}
}

func TestProductionCannotFallBackToTheDevelopmentSource(t *testing.T) {
	env := &stubProvider{name: ProviderEnv, value: devKey}
	ssm := &stubProvider{name: ProviderSSM, err: errors.New("access denied")}

	if _, _, err := Resolve(context.Background(),
		Config{Environment: EnvProduction, Ref: "/aegis/production/slasher_key"},
		allProviders(env, ssm), nil); err == nil {
		t.Fatal("production resolved a secret despite SSM failing")
	}
	if env.called != 0 {
		t.Fatal("production consulted the development source")
	}
}

// Even with SSM entirely unregistered — the shape of a misconfigured deploy — staging must fail
// rather than find another way.
func TestStagingFailsWhenItsProviderIsNotRegistered(t *testing.T) {
	env := &stubProvider{name: ProviderEnv, value: devKey}

	_, _, err := Resolve(context.Background(),
		Config{Environment: EnvStaging, Ref: "/aegis/staging/slasher_key"},
		map[string]Provider{ProviderEnv: env}, nil)

	if !errors.Is(err, ErrProviderMissing) {
		t.Fatalf("err = %v, want ErrProviderMissing", err)
	}
	if env.called != 0 {
		t.Fatal("an unregistered required provider fell through to the development source")
	}
}

// --- environment declaration ---

func TestEnvironmentMustBeDeclared(t *testing.T) {
	_, _, err := Resolve(context.Background(), Config{Ref: "SLASHER_KEY"},
		allProviders(&stubProvider{name: ProviderEnv, value: devKey}, &stubProvider{name: ProviderSSM}), nil)

	if !errors.Is(err, ErrEnvironmentUnset) {
		t.Fatalf("err = %v, want ErrEnvironmentUnset — an unset environment must not default to local", err)
	}
}

func TestUnknownEnvironmentIsRejected(t *testing.T) {
	for _, env := range []Environment{"dev", "prod", "Staging", "LOCAL", "test"} {
		_, _, err := Resolve(context.Background(), Config{Environment: env, Ref: "x"},
			allProviders(&stubProvider{name: ProviderEnv, value: devKey}, &stubProvider{name: ProviderSSM, value: devKey}), nil)

		if !errors.Is(err, ErrEnvironmentUnknown) {
			t.Errorf("environment %q: err = %v, want ErrEnvironmentUnknown", env, err)
		}
	}
}

func TestLocalUsesTheEnvironmentSource(t *testing.T) {
	env := &stubProvider{name: ProviderEnv, value: devKey}
	ssm := &stubProvider{name: ProviderSSM, value: "should not be read"}

	secret, provider, err := Resolve(context.Background(),
		Config{Environment: EnvLocal, Ref: "SLASHER_KEY"}, allProviders(env, ssm), nil)
	if err != nil {
		t.Fatalf("local should resolve: %v", err)
	}
	if provider != ProviderEnv {
		t.Errorf("provider = %q, want %q", provider, ProviderEnv)
	}
	if secret.Expose() != devKey {
		t.Error("local did not return the value the environment source supplied")
	}
	if ssm.called != 0 {
		t.Error("local consulted SSM")
	}
}

// --- no fallback on any failure mode ---

func TestEmptySecretFailsStartup(t *testing.T) {
	ssm := &stubProvider{name: ProviderSSM, value: ""}

	_, _, err := Resolve(context.Background(),
		Config{Environment: EnvStaging, Ref: "/aegis/staging/slasher_key"},
		allProviders(&stubProvider{name: ProviderEnv, value: devKey}, ssm), nil)

	if !errors.Is(err, ErrSecretEmpty) {
		t.Fatalf("err = %v, want ErrSecretEmpty — a present but empty parameter is a misconfiguration", err)
	}
}

func TestMalformedSecretFailsStartup(t *testing.T) {
	ssm := &stubProvider{name: ProviderSSM, value: "not-a-key"}
	validate := func(s Secret) error {
		if !strings.HasPrefix(s.Expose(), "0x") {
			return errors.New("not a hex key")
		}
		return nil
	}

	if _, _, err := Resolve(context.Background(),
		Config{Environment: EnvStaging, Ref: "/aegis/staging/slasher_key"},
		allProviders(&stubProvider{name: ProviderEnv, value: devKey}, ssm), validate); err == nil {
		t.Fatal("malformed material resolved; it must fail startup rather than surface later")
	}
}

func TestMissingReferenceFailsStartup(t *testing.T) {
	if _, _, err := Resolve(context.Background(),
		Config{Environment: EnvStaging},
		allProviders(&stubProvider{name: ProviderEnv, value: devKey}, &stubProvider{name: ProviderSSM, value: devKey}),
		nil); err == nil {
		t.Fatal("an unset reference resolved")
	}
}

// --- redaction ---

// Every path fmt and encoding/json can take must redact. A key reaching a log is a key that has to
// be rotated, and the reason is usually a %v somewhere nobody reviewed.
func TestSecretRedactsThroughEveryRenderingPath(t *testing.T) {
	secret := New(devKey)

	rendered := []string{
		secret.String(),
		fmt.Sprintf("%v", secret),
		fmt.Sprintf("%s", secret),
		fmt.Sprintf("%q", secret),
		fmt.Sprintf("%#v", secret),
		fmt.Sprint(secret),
	}

	encoded, err := json.Marshal(secret)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rendered = append(rendered, string(encoded))

	// A struct carrying the secret, which is how it would actually reach a log line.
	wrapped, err := json.Marshal(struct {
		Key Secret `json:"key"`
	}{Key: secret})
	if err != nil {
		t.Fatalf("marshal wrapper: %v", err)
	}
	rendered = append(rendered, string(wrapped))

	for i, out := range rendered {
		if strings.Contains(out, devKey) {
			t.Errorf("rendering %d leaked the secret: %s", i, out)
		}
		if strings.Contains(out, "ac0974") {
			t.Errorf("rendering %d leaked part of the secret: %s", i, out)
		}
	}
}

func TestExposeReturnsTheMaterial(t *testing.T) {
	if got := New(devKey).Expose(); got != devKey {
		t.Fatalf("Expose returned %q", got)
	}
}

// The error a failed resolve returns is logged. It must name the source and the reference without
// carrying material.
func TestResolveErrorsDoNotCarryMaterial(t *testing.T) {
	ssm := &stubProvider{name: ProviderSSM, value: devKey}
	validate := func(Secret) error { return errors.New("bad curve point") }

	_, _, err := Resolve(context.Background(),
		Config{Environment: EnvStaging, Ref: "/aegis/staging/slasher_key"},
		allProviders(&stubProvider{name: ProviderEnv}, ssm), validate)

	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), devKey) || strings.Contains(err.Error(), "ac0974") {
		t.Fatalf("error leaked material: %v", err)
	}
	if !strings.Contains(err.Error(), ProviderSSM) {
		t.Errorf("error should name the source it came from: %v", err)
	}
}

// The development source must not even be registered outside local. The policy already refuses to
// select it; this makes it absent, so two independent things would have to be wrong.
func TestDevelopmentProviderIsNotRegisteredOutsideLocal(t *testing.T) {
	for _, env := range []Environment{EnvStaging, EnvProduction} {
		providers, err := BuildProviders(context.Background(), env, "us-east-1")
		if err != nil {
			// Building the SSM client can fail without AWS configuration present, which is fine —
			// what must never happen is a map containing the environment source.
			continue
		}
		if _, present := providers[ProviderEnv]; present {
			t.Errorf("%s registered the development source", env)
		}
	}
}

func TestLocalRegistersOnlyTheDevelopmentProvider(t *testing.T) {
	providers, err := BuildProviders(context.Background(), EnvLocal, "")
	if err != nil {
		t.Fatalf("local should build without AWS configuration: %v", err)
	}
	if _, present := providers[ProviderEnv]; !present {
		t.Error("local did not register the environment source")
	}
	if _, present := providers[ProviderSSM]; present {
		t.Error("local registered SSM, which it has no configuration for")
	}
}

func TestBuildProvidersRejectsUndeclaredEnvironment(t *testing.T) {
	if _, err := BuildProviders(context.Background(), "", ""); !errors.Is(err, ErrEnvironmentUnset) {
		t.Fatalf("err = %v, want ErrEnvironmentUnset", err)
	}
	if _, err := BuildProviders(context.Background(), "prod", "us-east-1"); !errors.Is(err, ErrEnvironmentUnknown) {
		t.Fatalf("err = %v, want ErrEnvironmentUnknown", err)
	}
}

// A staging process that never had the variable in its environment is the intended deployment, but
// the guarantee must hold even when it does.
func TestStagingRefusesEvenWithTheDevelopmentVariablePresent(t *testing.T) {
	t.Setenv("SLASHER_PRIVATE_KEY", devKey)

	providers, err := BuildProviders(context.Background(), EnvStaging, "us-east-1")
	if err == nil {
		if _, present := providers[ProviderEnv]; present {
			t.Fatal("staging registered the environment source while the variable was set")
		}
	}

	// And with both registered by hand, resolution still refuses to use it.
	env := &stubProvider{name: ProviderEnv, value: devKey}
	if _, _, err := Resolve(context.Background(),
		Config{Environment: EnvStaging, Ref: "SLASHER_PRIVATE_KEY"},
		allProviders(env, &stubProvider{name: ProviderSSM, err: errors.New("not found")}),
		nil); err == nil {
		t.Fatal("staging resolved from the environment while the variable was set")
	}
	if env.called != 0 {
		t.Fatal("staging read the environment variable")
	}
}
