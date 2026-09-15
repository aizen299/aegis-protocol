//go:build localstack

// Exercises the real SSM provider — the real SDK, a real GetParameter, real SecureString
// decryption — against LocalStack rather than AWS.
//
// Everything else in this package tests the policy against a stub. That proves the policy and
// nothing about the code that actually reaches Parameter Store. Run with `make secrets-localstack`.
package secrets

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
)

const (
	localstackRegion = "us-east-1"
	stagingKey       = "0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
)

func endpoint(t *testing.T) string {
	t.Helper()
	url := os.Getenv("LOCALSTACK_ENDPOINT")
	if url == "" {
		t.Skip("LOCALSTACK_ENDPOINT is not set")
	}
	return url
}

// LocalStack accepts any credentials, but the SDK refuses to sign without some. Set explicitly so
// a developer's real profile is never what this test signs with.
func withCredentials(t *testing.T) {
	t.Helper()
	t.Setenv("AWS_ACCESS_KEY_ID", "localstack")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "localstack")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_PROFILE", "")
}

func rawClient(t *testing.T, url string) *ssm.Client {
	t.Helper()
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), awsconfig.WithRegion(localstackRegion))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return ssm.NewFromConfig(cfg, func(o *ssm.Options) { o.BaseEndpoint = aws.String(url) })
}

func putSecureString(t *testing.T, url, name, value string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	_, err := rawClient(t, url).PutParameter(ctx, &ssm.PutParameterInput{
		Name:      aws.String(name),
		Value:     aws.String(value),
		Type:      ssmtypes.ParameterTypeSecureString,
		Overwrite: aws.Bool(true),
	})
	if err != nil {
		t.Fatalf("put %s: %v", name, err)
	}
}

func stagingProvider(t *testing.T, url string) *SSMProvider {
	t.Helper()
	p, err := NewSSMProvider(context.Background(), SSMOptions{Region: localstackRegion, Endpoint: url})
	if err != nil {
		t.Fatalf("build provider: %v", err)
	}
	return p
}

// The staging path, end to end, against a real Parameter Store: a SecureString goes in, Resolve
// hands back the plaintext, and the provider it names is the one an operator would read in the
// startup log.
func TestStagingResolvesASecureStringFromRealParameterStore(t *testing.T) {
	url := endpoint(t)
	withCredentials(t)

	const ref = "/aegis/staging/slasher_private_key"
	putSecureString(t, url, ref, stagingKey)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	secret, provider, err := Resolve(ctx,
		Config{Environment: EnvStaging, Ref: ref},
		map[string]Provider{ProviderSSM: stagingProvider(t, url)},
		nil)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if provider != ProviderSSM {
		t.Errorf("provider = %q, want %q", provider, ProviderSSM)
	}
	// Not merely non-empty: a provider that forgot WithDecryption returns the stored ciphertext,
	// which is also non-empty and would pass a weaker assertion.
	if secret.Expose() != stagingKey {
		t.Errorf("resolved material does not match what was stored; decryption did not happen")
	}
}

func TestAnAbsentParameterFailsStartupWithoutLeaking(t *testing.T) {
	url := endpoint(t)
	withCredentials(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const ref = "/aegis/staging/never_created"
	_, provider, err := Resolve(ctx,
		Config{Environment: EnvStaging, Ref: ref},
		map[string]Provider{ProviderSSM: stagingProvider(t, url)},
		nil)
	if err == nil {
		t.Fatal("a missing parameter resolved")
	}
	if provider != ProviderSSM {
		t.Errorf("provider = %q, want the source that was tried", provider)
	}
	if !strings.Contains(err.Error(), ref) {
		t.Errorf("error does not name the reference that was missing: %v", err)
	}
}

func TestAnEmptyParameterFailsStartup(t *testing.T) {
	url := endpoint(t)
	withCredentials(t)

	// Parameter Store rejects an empty value, so the realistic malformed case is whitespace.
	const ref = "/aegis/staging/blank_key"
	putSecureString(t, url, ref, "   ")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, _, err := Resolve(ctx,
		Config{Environment: EnvStaging, Ref: ref},
		map[string]Provider{ProviderSSM: stagingProvider(t, url)},
		nil); !errors.Is(err, ErrSecretEmpty) {
		t.Fatalf("err = %v, want ErrSecretEmpty", err)
	}
}

// The no-fallback guarantee, with a working SSM present and the development variable set. This is
// the case a stub cannot argue away: the alternative source is genuinely reachable here.
func TestStagingNeverFallsBackWhileRealSSMIsReachable(t *testing.T) {
	url := endpoint(t)
	withCredentials(t)
	t.Setenv("SLASHER_PRIVATE_KEY", "0xdeadbeef")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	env := EnvProvider{}
	_, _, err := Resolve(ctx,
		Config{Environment: EnvStaging, Ref: "SLASHER_PRIVATE_KEY"},
		map[string]Provider{ProviderSSM: stagingProvider(t, url), ProviderEnv: env},
		nil)
	if err == nil {
		t.Fatal("staging resolved SLASHER_PRIVATE_KEY while the variable was set")
	}
	if strings.Contains(err.Error(), "deadbeef") {
		t.Fatalf("error leaked the development key: %v", err)
	}
}
