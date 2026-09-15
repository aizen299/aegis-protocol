package secrets

import (
	"context"
	"errors"
	"testing"
)

// The SSM client resolves these from the process environment at construction and logs nothing, so
// a redirected staging process would still report provider=aws-ssm. Startup must fail instead.
func TestAnAmbientEndpointOverrideFailsStartup(t *testing.T) {
	for _, name := range ambientEndpointVars {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "http://127.0.0.1:4566")

			_, err := NewSSMProvider(context.Background(), SSMOptions{Region: "us-east-1"})
			if !errors.Is(err, ErrEndpointAmbient) {
				t.Fatalf("err = %v, want ErrEndpointAmbient", err)
			}
		})
	}
}

func TestBuildProvidersRefusesAnAmbientEndpointOutsideLocal(t *testing.T) {
	t.Setenv("AWS_ENDPOINT_URL_SSM", "http://127.0.0.1:4566")

	for _, env := range []Environment{EnvStaging, EnvProduction} {
		if _, err := BuildProviders(context.Background(), env, "us-east-1"); !errors.Is(err, ErrEndpointAmbient) {
			t.Errorf("%s: err = %v, want ErrEndpointAmbient", env, err)
		}
	}
}

// An unset variable and a variable set to whitespace are the same thing. Treating the latter as a
// configured override would fail startup on a deploy template that renders an empty value.
func TestABlankEndpointVariableIsNotAnOverride(t *testing.T) {
	t.Setenv("AWS_ENDPOINT_URL_SSM", "   ")

	if _, err := NewSSMProvider(context.Background(), SSMOptions{Region: "us-east-1"}); errors.Is(err, ErrEndpointAmbient) {
		t.Fatal("a blank variable was treated as a configured endpoint")
	}
}

// The explicit knob is what the LocalStack suite uses. It must not be blocked by the guard that
// refuses the ambient form.
func TestAnExplicitEndpointIsHonouredWhileTheAmbientOneIsRefused(t *testing.T) {
	t.Setenv("AWS_ENDPOINT_URL_SSM", "http://127.0.0.1:4566")

	if _, err := NewSSMProvider(context.Background(), SSMOptions{
		Region:   "us-east-1",
		Endpoint: "http://127.0.0.1:4566",
	}); err != nil {
		t.Fatalf("explicit endpoint: %v", err)
	}
}
