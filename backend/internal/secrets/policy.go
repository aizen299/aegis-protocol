package secrets

import (
	"context"
	"errors"
	"fmt"
)

// Environment is declared by configuration, never inferred.
//
// Deciding "this must be development because the development variable happens to be set" is how a
// staging deploy ends up running on a developer's key, and how an incident becomes archaeology.
type Environment string

const (
	EnvLocal      Environment = "local"
	EnvStaging    Environment = "staging"
	EnvProduction Environment = "production"
)

// Provider names, logged at startup so an operator can confirm the source from logs alone.
const (
	ProviderEnv = "environment"
	ProviderSSM = "aws-ssm"
)

var (
	ErrEnvironmentUnset   = errors.New("APP_ENV is not set; the environment is declared, never inferred")
	ErrEnvironmentUnknown = errors.New("APP_ENV is not one of local, staging, production")
	ErrProviderMissing    = errors.New("no provider is registered for the source this environment requires")
	ErrSecretEmpty        = errors.New("the configured source returned an empty secret")
)

// Provider fetches material from one source.
type Provider interface {
	Name() string
	Fetch(ctx context.Context, ref string) (Secret, error)
}

// Validator rejects material that is present but unusable. A malformed key must fail startup, not
// surface later as an unsignable transaction.
type Validator func(Secret) error

// Config selects the source. Ref is interpreted by the provider: an environment variable name for
// local, a parameter path for SSM.
type Config struct {
	Environment Environment
	Ref         string
}

// providerFor maps an environment to its one permitted source.
//
// The mapping is total and has no default branch that widens: an unrecognised environment is an
// error rather than a guess. Local is the only environment where an environment variable is
// permitted at all.
func providerFor(env Environment) (string, error) {
	switch env {
	case EnvLocal:
		return ProviderEnv, nil
	case EnvStaging, EnvProduction:
		return ProviderSSM, nil
	case "":
		return "", ErrEnvironmentUnset
	default:
		return "", fmt.Errorf("%w: %q", ErrEnvironmentUnknown, env)
	}
}

// Resolve fetches the secret the environment's source is required to supply.
//
// There is no fallback anywhere in this function. If the required provider is not registered, the
// fetch fails, the value is empty, or validation rejects it, Resolve returns an error and the
// caller is expected to refuse to start. A service that degrades to a weaker source is worse than
// one that will not start, because the degradation is silent and the weaker source is usually a
// development key.
//
// Returns the provider name so the caller can log where the material came from. It never returns
// the material in a loggable form.
func Resolve(ctx context.Context, cfg Config, providers map[string]Provider, validate Validator) (Secret, string, error) {
	name, err := providerFor(cfg.Environment)
	if err != nil {
		return Secret{}, "", err
	}

	provider, ok := providers[name]
	if !ok {
		return Secret{}, name, fmt.Errorf("%w: environment %q requires %q", ErrProviderMissing, cfg.Environment, name)
	}
	if cfg.Ref == "" {
		return Secret{}, name, fmt.Errorf("no reference configured for provider %q", name)
	}

	secret, err := provider.Fetch(ctx, cfg.Ref)
	if err != nil {
		return Secret{}, name, fmt.Errorf("fetch from %q: %w", name, err)
	}
	if secret.IsZero() {
		return Secret{}, name, fmt.Errorf("%w: provider %q, reference %q", ErrSecretEmpty, name, cfg.Ref)
	}

	if validate != nil {
		if err := validate(secret); err != nil {
			return Secret{}, name, fmt.Errorf("secret from %q is malformed: %w", name, err)
		}
	}

	return secret, name, nil
}
