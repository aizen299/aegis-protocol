package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// EnvProvider reads material from a process environment variable.
//
// Registered only for local development. Registering it in a staging or production process is
// pointless rather than dangerous — providerFor never selects it there — but the wiring keeps it
// out of those processes anyway, so the material is not in the environment at all.
type EnvProvider struct{}

func (EnvProvider) Name() string { return ProviderEnv }

func (EnvProvider) Fetch(_ context.Context, ref string) (Secret, error) {
	value, ok := os.LookupEnv(ref)
	if !ok {
		return Secret{}, fmt.Errorf("environment variable %q is not set", ref)
	}
	return New(strings.TrimSpace(value)), nil
}

// SSMProvider reads material from AWS SSM Parameter Store, decrypting SecureString parameters.
type SSMProvider struct {
	client ssmClient
}

// ssmClient is the one call this provider makes, so tests do not need the AWS SDK.
type ssmClient interface {
	GetParameter(ctx context.Context, in *ssm.GetParameterInput, opts ...func(*ssm.Options)) (*ssm.GetParameterOutput, error)
}

// NewSSMProvider builds a provider from ambient AWS configuration — task role on ECS, profile
// locally. Failing here fails startup, which is the intent: a service that cannot reach its secret
// source must not run.
func NewSSMProvider(ctx context.Context, region string) (*SSMProvider, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	return &SSMProvider{client: ssm.NewFromConfig(cfg)}, nil
}

func (SSMProvider) Name() string { return ProviderSSM }

func (p *SSMProvider) Fetch(ctx context.Context, ref string) (Secret, error) {
	decrypt := true
	out, err := p.client.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           &ref,
		WithDecryption: &decrypt,
	})
	if err != nil {
		return Secret{}, fmt.Errorf("get parameter %q: %w", ref, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return Secret{}, fmt.Errorf("parameter %q has no value", ref)
	}
	return New(strings.TrimSpace(*out.Parameter.Value)), nil
}

// BuildProviders registers only the source the environment is permitted to use.
//
// Belt and braces with providerFor: the policy already refuses to select the environment source
// outside local, and this makes it absent there as well. Two independent things would have to be
// wrong for a staging process to read a key out of its environment.
func BuildProviders(ctx context.Context, env Environment, region string) (map[string]Provider, error) {
	switch env {
	case EnvLocal:
		return map[string]Provider{ProviderEnv: EnvProvider{}}, nil

	case EnvStaging, EnvProduction:
		if region == "" {
			return nil, fmt.Errorf("AWS_REGION is required in %s", env)
		}
		provider, err := NewSSMProvider(ctx, region)
		if err != nil {
			return nil, err
		}
		return map[string]Provider{ProviderSSM: provider}, nil

	case "":
		return nil, ErrEnvironmentUnset
	default:
		return nil, fmt.Errorf("%w: %q", ErrEnvironmentUnknown, env)
	}
}
