package bedrock_test

import (
	"context"
	"crypto/sha1" //nolint:gosec // how the AWS tools name the cache file
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/aws/smithy-go"

	"github.com/Marcel-Goehn/kraftwerk/internal/bedrock"
)

var start = time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)

// fakeSTS answers like AWS STS: each call hands out the next numbered
// credentials, valid for what was asked for from now.
type fakeSTS struct {
	now   func() time.Time
	calls []string
	n     int

	federationErr, sessionErr, assumeErr, identityErr error

	federation *sts.GetFederationTokenInput
	assume     *sts.AssumeRoleInput
}

func (f *fakeSTS) next(duration *int32) *types.Credentials {
	f.n++
	suffix := strconv.Itoa(f.n)
	return &types.Credentials{
		AccessKeyId:     aws.String("ASIA" + suffix),
		SecretAccessKey: aws.String("minted-secret-" + suffix),
		SessionToken:    aws.String("minted-token-" + suffix),
		Expiration:      aws.Time(f.now().Add(time.Duration(*duration) * time.Second)),
	}
}

func (f *fakeSTS) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	f.calls = append(f.calls, "GetCallerIdentity")
	if f.identityErr != nil {
		return nil, f.identityErr
	}
	return &sts.GetCallerIdentityOutput{Arn: aws.String("arn:aws:iam::123:user/dev")}, nil
}

func (f *fakeSTS) GetFederationToken(_ context.Context, in *sts.GetFederationTokenInput, _ ...func(*sts.Options)) (*sts.GetFederationTokenOutput, error) {
	f.calls = append(f.calls, "GetFederationToken")
	f.federation = in
	if f.federationErr != nil {
		return nil, f.federationErr
	}
	return &sts.GetFederationTokenOutput{Credentials: f.next(in.DurationSeconds)}, nil
}

func (f *fakeSTS) GetSessionToken(_ context.Context, in *sts.GetSessionTokenInput, _ ...func(*sts.Options)) (*sts.GetSessionTokenOutput, error) {
	f.calls = append(f.calls, "GetSessionToken")
	if f.sessionErr != nil {
		return nil, f.sessionErr
	}
	return &sts.GetSessionTokenOutput{Credentials: f.next(in.DurationSeconds)}, nil
}

func (f *fakeSTS) AssumeRole(_ context.Context, in *sts.AssumeRoleInput, _ ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	f.calls = append(f.calls, "AssumeRole")
	f.assume = in
	if f.assumeErr != nil {
		return nil, f.assumeErr
	}
	return &sts.AssumeRoleOutput{Credentials: f.next(in.DurationSeconds)}, nil
}

// exported is the file's content, the shape Claude Code reads.
type exported struct {
	Credentials struct {
		AccessKeyId, SecretAccessKey, SessionToken, Expiration string
	}
}

func read(t *testing.T, k *bedrock.Keeper) exported {
	t.Helper()
	data, err := os.ReadFile(k.File())
	if err != nil {
		t.Fatal(err)
	}
	var e exported
	if err := json.Unmarshal(data, &e); err != nil {
		t.Fatalf("the file is not JSON: %v", err)
	}
	return e
}

// world is a Keeper's surroundings, with a clock the test moves.
type world struct {
	now      time.Time
	sts      *fakeSTS
	secrets  []string
	warnings []string
	dir      string
	timers   chan timer
}

// timer is one wait the Keeper started.
type timer struct {
	d    time.Duration
	fire chan time.Time
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{now: start, dir: filepath.Join(t.TempDir(), "credentials"), timers: make(chan timer, 8)}
	w.sts = &fakeSTS{now: func() time.Time { return w.now }}
	return w
}

func (w *world) delivery() bedrock.Delivery {
	return bedrock.Delivery{
		Dir:    w.dir,
		Secret: func(s string) { w.secrets = append(w.secrets, s) },
		Warn:   func(s string) { w.warnings = append(w.warnings, s) },
		Now:    func() time.Time { return w.now },
		After: func(d time.Duration) <-chan time.Time {
			fire := make(chan time.Time)
			w.timers <- timer{d, fire}
			return fire
		},
	}
}

func (w *world) start(t *testing.T, cfg bedrock.Config) *bedrock.Keeper {
	t.Helper()
	k, err := bedrock.NewWith(cfg, w.sts, nil).Start(context.Background(), w.delivery())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(k.Stop)
	return k
}

func TestMintsABedrockOnlyFederationTokenIntoTheFile(t *testing.T) {
	w := newWorld(t)

	k := w.start(t, bedrock.Config{Region: "eu-central-1"})

	got := read(t, k)
	if got.Credentials.AccessKeyId != "ASIA1" || got.Credentials.SecretAccessKey != "minted-secret-1" || got.Credentials.SessionToken != "minted-token-1" {
		t.Errorf("file = %+v, want the minted credentials", got)
	}
	if want := start.Add(time.Hour).Format(time.RFC3339); got.Credentials.Expiration != want {
		t.Errorf("Expiration = %q, want %q", got.Credentials.Expiration, want)
	}
	wantBedrockOnly(t, aws.ToString(w.sts.federation.Policy))
	for _, s := range []string{"ASIA1", "minted-secret-1", "minted-token-1"} {
		if !strings.Contains(strings.Join(w.secrets, " "), s) {
			t.Errorf("secrets = %q, want %q redacted", w.secrets, s)
		}
	}
}

// wantBedrockOnly fails unless the policy allows InvokeModel and nothing
// outside Bedrock.
func wantBedrockOnly(t *testing.T, policy string) {
	t.Helper()
	var doc struct {
		Statement []struct {
			Effect string
			Action []string
		}
	}
	if err := json.Unmarshal([]byte(policy), &doc); err != nil {
		t.Fatalf("policy %q is not JSON: %v", policy, err)
	}
	if len(doc.Statement) != 1 || doc.Statement[0].Effect != "Allow" || !slices.Contains(doc.Statement[0].Action, "bedrock:InvokeModel") {
		t.Fatalf("policy = %s, want one Allow of bedrock:InvokeModel", policy)
	}
	for _, a := range doc.Statement[0].Action {
		if !strings.HasPrefix(a, "bedrock:") || strings.Contains(a, "*") {
			t.Errorf("policy allows %q, want only named Bedrock actions", a)
		}
	}
}

var (
	accessDenied = &smithy.GenericAPIError{Code: "AccessDenied", Message: "Cannot call this with session credentials"}
	throttled    = &smithy.GenericAPIError{Code: "Throttling", Message: "Rate exceeded"}
)

func TestFallsBackToASessionTokenWhenFederationIsRefused(t *testing.T) {
	w := newWorld(t)
	w.sts.federationErr = accessDenied

	k := w.start(t, bedrock.Config{Region: "eu-central-1"})

	if got := read(t, k).Credentials.SessionToken; got != "minted-token-1" {
		t.Errorf("SessionToken = %q, want the minted one", got)
	}
	if want := []string{"GetFederationToken", "GetSessionToken"}; !slices.Equal(w.sts.calls, want) {
		t.Errorf("calls = %q, want %q", w.sts.calls, want)
	}
	if len(w.warnings) != 1 || !strings.Contains(w.warnings[0], "not limited to Bedrock") {
		t.Errorf("warnings = %q, want one saying the token isn't limited to Bedrock", w.warnings)
	}
}

func TestAFederationFailureThatIsNoRefusalDoesNotWidenTheCredentials(t *testing.T) {
	w := newWorld(t)
	w.sts.federationErr = throttled

	_, err := bedrock.NewWith(bedrock.Config{}, w.sts, nil).Start(context.Background(), w.delivery())

	if err == nil || !strings.Contains(err.Error(), "Throttling") {
		t.Errorf("err = %v, want the throttling error", err)
	}
	if slices.Contains(w.sts.calls, "GetSessionToken") {
		t.Errorf("calls = %q, want no GetSessionToken", w.sts.calls)
	}
}

func TestWhenBothAreRefusedTheErrorNamesTheWayOut(t *testing.T) {
	w := newWorld(t)
	w.sts.federationErr, w.sts.sessionErr = accessDenied, accessDenied

	_, err := bedrock.NewWith(bedrock.Config{}, w.sts, nil).Start(context.Background(), w.delivery())

	if err == nil || !strings.Contains(err.Error(), "role_arn") || !strings.Contains(err.Error(), "i_accept_long_lived_keys_in_sandbox") {
		t.Errorf("err = %v, want it to name role_arn and the acknowledgement", err)
	}
}

func TestARoleIsAssumedWithABedrockOnlySessionPolicy(t *testing.T) {
	w := newWorld(t)
	const role = "arn:aws:iam::123:role/factory"

	k := w.start(t, bedrock.Config{Region: "eu-central-1", RoleARN: role})

	if got := read(t, k).Credentials.AccessKeyId; got != "ASIA1" {
		t.Errorf("AccessKeyId = %q, want the minted one", got)
	}
	if want := []string{"AssumeRole"}; !slices.Equal(w.sts.calls, want) {
		t.Errorf("calls = %q, want %q", w.sts.calls, want)
	}
	if got := aws.ToString(w.sts.assume.RoleArn); got != role {
		t.Errorf("RoleArn = %q, want %q", got, role)
	}
	wantBedrockOnly(t, aws.ToString(w.sts.assume.Policy))
}

func source(c aws.Credentials) aws.CredentialsProvider {
	return aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) { return c, nil })
}

func TestLongLivedKeysPassedThroughGoInTheEnvAndNoFile(t *testing.T) {
	w := newWorld(t)
	own := aws.Credentials{AccessKeyID: "AKIAOWN", SecretAccessKey: "own-secret"}

	k, err := bedrock.NewWith(bedrock.Config{PassThrough: true}, w.sts, source(own)).Start(context.Background(), w.delivery())
	if err != nil {
		t.Fatal(err)
	}
	defer k.Stop()

	env := k.Env()
	if env["AWS_ACCESS_KEY_ID"] != "AKIAOWN" || env["AWS_SECRET_ACCESS_KEY"] != "own-secret" || len(env) != 2 {
		t.Errorf("env = %v, want the two keys", env)
	}
	if len(w.sts.calls) != 0 {
		t.Errorf("calls = %q, want none", w.sts.calls)
	}
	if _, err := os.Stat(k.File()); !os.IsNotExist(err) {
		t.Errorf("stat file: %v, want no file: Claude Code refuses one without a session token", err)
	}
	if !strings.Contains(strings.Join(w.secrets, " "), "own-secret") {
		t.Errorf("secrets = %q, want the key redacted", w.secrets)
	}
}

func TestTemporaryCredentialsPassedThroughGoInTheFile(t *testing.T) {
	w := newWorld(t)
	own := aws.Credentials{AccessKeyID: "ASIAOWN", SecretAccessKey: "own-secret", SessionToken: "own-token", CanExpire: true, Expires: start.Add(4 * time.Hour)}

	k, err := bedrock.NewWith(bedrock.Config{PassThrough: true}, w.sts, source(own)).Start(context.Background(), w.delivery())
	if err != nil {
		t.Fatal(err)
	}
	defer k.Stop()

	if got := read(t, k).Credentials.SessionToken; got != "own-token" {
		t.Errorf("SessionToken = %q, want the user's own", got)
	}
	if len(k.Env()) != 0 {
		t.Errorf("env = %v, want none", k.Env())
	}
}

func TestTheTestCallAsksWhoTheCredentialsBelongTo(t *testing.T) {
	w := newWorld(t)

	if err := bedrock.NewWith(bedrock.Config{RoleARN: "arn:aws:iam::123:role/x"}, w.sts, nil).Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"GetCallerIdentity"}; !slices.Equal(w.sts.calls, want) {
		t.Errorf("calls = %q, want only the free %q", w.sts.calls, want)
	}
}

func TestTheTestCallPassesOnARefusal(t *testing.T) {
	w := newWorld(t)
	w.sts.identityErr = &smithy.GenericAPIError{Code: "InvalidClientTokenId", Message: "bad"}

	err := bedrock.NewWith(bedrock.Config{}, w.sts, nil).Test(context.Background())

	if err == nil || !strings.Contains(err.Error(), "InvalidClientTokenId") {
		t.Errorf("err = %v, want the refusal", err)
	}
}

// next waits for the Keeper's next wait.
func (w *world) next(t *testing.T) timer {
	t.Helper()
	select {
	case tm := <-w.timers:
		return tm
	case <-time.After(5 * time.Second):
		t.Fatal("the Keeper never started waiting")
		return timer{}
	}
}

func TestRewritesTheFileBeforeTheCredentialsExpire(t *testing.T) {
	w := newWorld(t)
	k := w.start(t, bedrock.Config{Region: "eu-central-1"})

	wait := w.next(t)
	if wait.d != 30*time.Minute {
		t.Errorf("first wait = %s, want 30m: half an hour left is what Claude Code may cache", wait.d)
	}
	w.now = w.now.Add(wait.d)
	wait.fire <- w.now
	again := w.next(t) // it waits again only once the file is rewritten

	got := read(t, k).Credentials
	if got.AccessKeyId != "ASIA2" || got.SessionToken != "minted-token-2" {
		t.Errorf("file = %+v, want the second credentials", got)
	}
	if want := w.now.Add(time.Hour).Format(time.RFC3339); got.Expiration != want {
		t.Errorf("Expiration = %q, want %q", got.Expiration, want)
	}
	if !strings.Contains(strings.Join(w.secrets, " "), "minted-secret-2") {
		t.Errorf("secrets = %q, want the new secret redacted", w.secrets)
	}
	if again.d != 30*time.Minute {
		t.Errorf("next wait = %s, want 30m", again.d)
	}
}

func TestShortLivedCredentialsAreRefreshedAtHalfTheirLife(t *testing.T) {
	w := newWorld(t)
	own := aws.Credentials{AccessKeyID: "ASIAOWN", SecretAccessKey: "s", SessionToken: "t", CanExpire: true, Expires: start.Add(20 * time.Minute)}

	k, err := bedrock.NewWith(bedrock.Config{PassThrough: true}, w.sts, source(own)).Start(context.Background(), w.delivery())
	if err != nil {
		t.Fatal(err)
	}
	defer k.Stop()

	if got := w.next(t).d; got != 10*time.Minute {
		t.Errorf("wait = %s, want 10m", got)
	}
}

func TestAFailedRefreshWarnsOnceRetriesAndKeepsTheOldFile(t *testing.T) {
	w := newWorld(t)
	k := w.start(t, bedrock.Config{Region: "eu-central-1"})

	wait := w.next(t)
	w.sts.federationErr = throttled
	wait.fire <- w.now
	retry := w.next(t)
	if retry.d != time.Minute {
		t.Errorf("retry wait = %s, want 1m", retry.d)
	}
	if got := read(t, k).Credentials.AccessKeyId; got != "ASIA1" {
		t.Errorf("AccessKeyId = %q, want the old credentials kept", got)
	}
	retry.fire <- w.now
	retry = w.next(t)
	if len(w.warnings) != 1 || !strings.Contains(w.warnings[0], "refreshing") || !strings.Contains(w.warnings[0], "Throttling") {
		t.Errorf("warnings = %q, want one about the refresh", w.warnings)
	}

	w.sts.federationErr = nil
	retry.fire <- w.now
	w.next(t)
	if got := read(t, k).Credentials.AccessKeyId; got != "ASIA2" {
		t.Errorf("AccessKeyId = %q, want the refreshed credentials", got)
	}
}

func TestTheSessionTokenWarningComesOnceAcrossRefreshes(t *testing.T) {
	w := newWorld(t)
	w.sts.federationErr = accessDenied
	w.start(t, bedrock.Config{Region: "eu-central-1"})

	wait := w.next(t)
	wait.fire <- w.now
	w.next(t)

	if len(w.warnings) != 1 {
		t.Errorf("warnings = %q, want one", w.warnings)
	}
}

func TestStopRemovesTheFileAndEndsTheRefreshing(t *testing.T) {
	w := newWorld(t)
	k := w.start(t, bedrock.Config{Region: "eu-central-1"})
	w.next(t)

	k.Stop()

	if _, err := os.Stat(k.File()); !os.IsNotExist(err) {
		t.Errorf("stat file: %v, want it gone", err)
	}
	k.Stop() // a second Stop is harmless
}

func TestLongLivedKeysNeedNoRefreshing(t *testing.T) {
	w := newWorld(t)
	own := aws.Credentials{AccessKeyID: "AKIAOWN", SecretAccessKey: "own-secret"}

	k, err := bedrock.NewWith(bedrock.Config{PassThrough: true}, w.sts, source(own)).Start(context.Background(), w.delivery())
	if err != nil {
		t.Fatal(err)
	}
	k.Stop()

	if len(w.timers) != 0 {
		t.Errorf("the Keeper started %d waits, want none", len(w.timers))
	}
}

// ssoProfile sets up a home with an SSO profile "dev" and its cached token.
func ssoProfile(t *testing.T, profile, token string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	config := filepath.Join(home, "config")
	t.Setenv("AWS_CONFIG_FILE", config)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(home, "credentials"))
	if err := os.WriteFile(config, []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum([]byte("corp")) //nolint:gosec // how the AWS tools name the cache file
	cache := filepath.Join(home, ".aws", "sso", "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, hex.EncodeToString(sum[:])+".json"), []byte(token), 0o600); err != nil {
		t.Fatal(err)
	}
}

const ssoSessionProfile = "[profile dev]\nsso_session = corp\nsso_account_id = 123\nsso_role_name = dev\n[sso-session corp]\nsso_start_url = https://corp.awsapps.com/start\nsso_region = eu-central-1\n"

// legacySSOProfile keys its cached token by this start URL.
const legacySSOProfile = "[profile dev]\nsso_start_url = corp\nsso_region = eu-central-1\nsso_account_id = 123\nsso_role_name = dev\n"

func tokenEnding(at time.Time, more string) string {
	return `{"accessToken":"t","expiresAt":"` + at.Format(time.RFC3339) + `"` + more + `}`
}

func warningsOf(cfg bedrock.Config) []string {
	return bedrock.NewWith(cfg, nil, nil).Warnings(context.Background(), start)
}

func TestWarnsWhenTheSSOSessionEndsWithinTwoHours(t *testing.T) {
	ssoProfile(t, legacySSOProfile, tokenEnding(start.Add(90*time.Minute), ""))

	got := warningsOf(bedrock.Config{Profile: "dev"})

	if len(got) != 1 || !strings.Contains(got[0], "aws sso login --profile dev") || !strings.Contains(got[0], "1h30m") {
		t.Errorf("warnings = %q, want one naming the time left and the login", got)
	}
}

func TestNoWarningWhenTheSSOSessionLastsLonger(t *testing.T) {
	ssoProfile(t, legacySSOProfile, tokenEnding(start.Add(3*time.Hour), ""))

	if got := warningsOf(bedrock.Config{Profile: "dev"}); len(got) != 0 {
		t.Errorf("warnings = %q, want none", got)
	}
}

func TestSSOSessionTokensAreFoundByTheSessionName(t *testing.T) {
	ssoProfile(t, ssoSessionProfile, tokenEnding(start.Add(time.Hour), ""))

	if got := warningsOf(bedrock.Config{Profile: "dev"}); len(got) != 1 {
		t.Errorf("warnings = %q, want one", got)
	}
}

// A refreshable token's expiresAt is an hour away whatever the session has
// left, so it says nothing about when the session ends.
func TestARefreshableTokenSaysNothingAboutTheSessionsEnd(t *testing.T) {
	ssoProfile(t, ssoSessionProfile, tokenEnding(start.Add(time.Hour), `,"refreshToken":"r"`))

	if got := warningsOf(bedrock.Config{Profile: "dev"}); len(got) != 0 {
		t.Errorf("warnings = %q, want none", got)
	}
}

func TestAProfileWithoutSSOGetsNoWarning(t *testing.T) {
	ssoProfile(t, "[profile dev]\nregion = eu-central-1\n", "{}")

	if got := warningsOf(bedrock.Config{Profile: "dev"}); len(got) != 0 {
		t.Errorf("warnings = %q, want none", got)
	}
}

func TestPassingCredentialsThroughWarnsEveryTime(t *testing.T) {
	got := warningsOf(bedrock.Config{PassThrough: true, AccessKeyID: "AKIA", SecretAccessKey: "s"})

	if len(got) != 1 || !strings.Contains(got[0], "i_accept_long_lived_keys_in_sandbox") {
		t.Errorf("warnings = %q, want one naming the acknowledgement", got)
	}
}

func TestPassedThroughCredentialsAreFetchedAgainNotServedFromTheSDKsCache(t *testing.T) {
	w := newWorld(t)
	fetched := 0
	own := aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		fetched++
		return aws.Credentials{AccessKeyID: "ASIAOWN" + strconv.Itoa(fetched), SecretAccessKey: "s", SessionToken: "t", CanExpire: true, Expires: time.Now().Add(time.Hour)}, nil
	})

	k, err := bedrock.NewWith(bedrock.Config{PassThrough: true}, w.sts, aws.NewCredentialsCache(own)).Start(context.Background(), w.delivery())
	if err != nil {
		t.Fatal(err)
	}
	defer k.Stop()
	w.next(t).fire <- w.now
	w.next(t)

	if got := read(t, k).Credentials.AccessKeyId; got != "ASIAOWN2" {
		t.Errorf("AccessKeyId = %q, want the credentials fetched for the refresh", got)
	}
}
