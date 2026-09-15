package secrets

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// ambientEndpointVars redirect the SSM client at construction, from the process environment, with
// nothing logged. See ssm/endpoints.go resolveBaseEndpoint. A staging process that read its key
// from an attacker's endpoint would still log provider=aws-ssm, so these are refused rather than
// honoured: outside local, the endpoint is AWS or the process does not start.
var ambientEndpointVars = []string{"AWS_ENDPOINT_URL_SSM", "AWS_ENDPOINT_URL"}

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

// SSMOptions configures the SSM client.
//
// Endpoint points at a non-AWS Parameter Store implementation. It exists so the real provider can
// be exercised against LocalStack, and BuildProviders refuses it outside local.
type SSMOptions struct {
	Region   string
	Endpoint string
}

// NewSSMProvider builds a provider from ambient AWS configuration — task role on ECS, profile
// locally. Failing here fails startup, which is the intent: a service that cannot reach its secret
// source must not run.
func NewSSMProvider(ctx context.Context, opts SSMOptions) (*SSMProvider, error) {
	if opts.Endpoint == "" {
		if name, set := ambientEndpointOverride(); set {
			return nil, fmt.Errorf("%w: %s is set", ErrEndpointAmbient, name)
		}
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(opts.Region))
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	client := ssm.NewFromConfig(cfg, func(o *ssm.Options) {
		if opts.Endpoint != "" {
			o.BaseEndpoint = aws.String(opts.Endpoint)
		}
	})
	return &SSMProvider{client: client}, nil
}

func ambientEndpointOverride() (string, bool) {
	for _, name := range ambientEndpointVars {
		if v, ok := os.LookupEnv(name); ok && strings.TrimSpace(v) != "" {
			return name, true
		}
	}
	return "", false
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
		provider, err := NewSSMProvider(ctx, SSMOptions{Region: region})
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
