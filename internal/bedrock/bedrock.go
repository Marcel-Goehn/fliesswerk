// Package bedrock keeps a user's AWS credentials on the host. It turns them
// into short-lived, Bedrock-only ones with the AWS SDK built into the binary,
// and keeps those in a file that the Sandbox can only read, rewriting it
// before they expire.
package bedrock

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/ssocreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/aws/smithy-go"

	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox"
)

// lifetime is how long minted credentials last. An hour is what every path
// accepts: a role's default limit, and a root user's.
const lifetime = time.Hour

// Refreshing. Claude Code caches credentials until shortly before their
// Expiration, so the file always has a good while left in it.
const (
	refreshBefore = 30 * time.Minute
	retryEvery    = time.Minute
)

// ssoWarnWithin is how soon an SSO session may end before a Run says so.
const ssoWarnWithin = 2 * time.Hour

// sessionName names what is minted, in the user's CloudTrail.
const sessionName = "fliesswerk-bedrock"

// policy is all that minted credentials may do. What they can do is also
// limited to what the credentials they come from can do.
const policy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":[` +
	`"bedrock:InvokeModel","bedrock:InvokeModelWithResponseStream",` +
	`"bedrock:GetInferenceProfile","bedrock:ListInferenceProfiles"],"Resource":"*"}]}`

// STS is the part of AWS STS the Factory calls. *sts.Client is one.
type STS interface {
	GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error)
	GetFederationToken(context.Context, *sts.GetFederationTokenInput, ...func(*sts.Options)) (*sts.GetFederationTokenOutput, error)
	GetSessionToken(context.Context, *sts.GetSessionTokenInput, ...func(*sts.Options)) (*sts.GetSessionTokenOutput, error)
	AssumeRole(context.Context, *sts.AssumeRoleInput, ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

// Config is the `method = "bedrock"` settings, with its secrets resolved.
type Config struct {
	Region, Profile              string
	AccessKeyID, SecretAccessKey string
	// RoleARN is the role to assume, "" for none.
	RoleARN string
	// PassThrough hands the user's own credentials over as they are.
	PassThrough bool
}

// Client is what can sign in to AWS for one Config.
type Client struct {
	cfg    Config
	sts    STS
	source aws.CredentialsProvider
}

// New is a Client that signs in the way cfg says, with the AWS SDK. It calls
// nothing yet, but reads the AWS config files.
func New(ctx context.Context, cfg Config) (*Client, error) {
	opts := []func(*config.LoadOptions) error{config.WithRegion(cfg.Region)}
	if cfg.Profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(cfg.Profile))
	}
	if cfg.AccessKeyID != "" {
		opts = append(opts, config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")))
	}
	loaded, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("reading the AWS config: %w", err)
	}
	return NewWith(cfg, sts.NewFromConfig(loaded), loaded.Credentials), nil
}

// NewWith is a Client on this STS, where source is where the user's own
// credentials come from.
func NewWith(cfg Config, s STS, source aws.CredentialsProvider) *Client {
	return &Client{cfg: cfg, sts: s, source: source}
}

// Delivery says where a Keeper keeps its file and whom it tells.
type Delivery struct {
	// Dir holds the file. The Keeper owns it: it starts out empty.
	Dir string
	// Secret learns each secret value before anything can show it.
	Secret func(string)
	// Warn gets a warning for a person.
	Warn func(string)
	// Now and After are the clock, nil for the real one.
	Now   func() time.Time
	After func(time.Duration) <-chan time.Time
}

// Keeper holds a Run's AWS credentials.
type Keeper struct {
	client *Client
	d      Delivery
	// env is what each Step's env gets, for credentials the file can't carry.
	env map[string]string
	// warned is set once the session token fallback was warned about.
	warned bool
	// cancel ends the refreshing and done says it has ended, nil for
	// credentials that aren't refreshed.
	cancel context.CancelFunc
	done   chan struct{}
}

// Start mints the first credentials and delivers them, so they are there
// when it returns. Stop ends what this starts.
func (c *Client) Start(ctx context.Context, d Delivery) (*Keeper, error) {
	if err := os.RemoveAll(d.Dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(d.Dir, 0o700); err != nil {
		return nil, err
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.After == nil {
		d.After = time.After
	}
	k := &Keeper{client: c, d: d}
	creds, err := k.mint(ctx)
	if err != nil {
		return nil, err
	}
	if creds.SessionToken == "" {
		// Claude Code takes nothing from the file without a session token.
		// Keys like these don't expire either, so they need no refreshing.
		k.tellRedactor(creds)
		k.env = map[string]string{"AWS_ACCESS_KEY_ID": creds.AccessKeyID, "AWS_SECRET_ACCESS_KEY": creds.SecretAccessKey}
		return k, nil
	}
	if err := k.write(creds); err != nil {
		return nil, err
	}
	// A Run being stopped still needs its credentials until Stop.
	refreshing, cancel := context.WithCancel(context.WithoutCancel(ctx))
	k.cancel, k.done = cancel, make(chan struct{})
	go k.refresh(refreshing, creds)
	return k, nil
}

// refresh replaces the credentials before they expire until ctx ends. A
// failure keeps the old file, which may well still be good, and tries again.
func (k *Keeper) refresh(ctx context.Context, creds aws.Credentials) {
	defer close(k.done)
	wait, failing := k.untilRefresh(creds), false
	for {
		select {
		case <-ctx.Done():
			return
		case <-k.d.After(wait):
		}
		next, err := k.mint(ctx)
		if err == nil {
			err = k.write(next)
		}
		switch {
		case ctx.Err() != nil:
			return
		case err == nil:
			creds, failing = next, false
			wait = k.untilRefresh(creds)
		default:
			if !failing {
				k.d.Warn(fmt.Sprintf("refreshing the AWS credentials failed, trying again every minute; Steps start failing when the current ones expire: %v", err))
			}
			failing, wait = true, retryEvery
		}
	}
}

// untilRefresh is how long creds can stay in the file.
func (k *Keeper) untilRefresh(creds aws.Credentials) time.Duration {
	if !creds.CanExpire {
		return refreshBefore
	}
	left := creds.Expires.Sub(k.d.Now())
	return max(left-refreshBefore, left/2, retryEvery)
}

// File is where the credentials are on the host.
func (k *Keeper) File() string { return filepath.Join(k.d.Dir, sandbox.CredentialFile) }

// Env is what each Step's env gets, none when the credentials are in the
// file.
func (k *Keeper) Env() map[string]string { return k.env }

// InFile reports whether the credentials are in the file, not the env.
func (k *Keeper) InFile() bool { return k.env == nil }

// Stop ends the refreshing and removes the file. It can be called twice.
func (k *Keeper) Stop() {
	if k.cancel != nil {
		k.cancel()
		<-k.done
	}
	_ = os.RemoveAll(k.d.Dir)
}

// Test makes the free call that proves the user's credentials work.
func (c *Client) Test(ctx context.Context) error {
	if _, err := c.sts.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{}); err != nil {
		return fmt.Errorf("AWS STS GetCallerIdentity: %w", err)
	}
	return nil
}

// Warnings are what a person should hear at the start of every Run.
func (c *Client) Warnings(ctx context.Context, now time.Time) []string {
	var warnings []string
	if c.cfg.PassThrough {
		warnings = append(warnings, "i_accept_long_lived_keys_in_sandbox is set: your own AWS credentials go into every Sandbox as they are, not limited to Bedrock; the agent can read them")
	}
	if end, ok := c.ssoSessionEnd(ctx); ok {
		if left := end.Sub(now); left > 0 && left <= ssoWarnWithin {
			warnings = append(warnings, fmt.Sprintf("the AWS SSO session of profile %q ends in %s, and a Run still going then fails; run `aws sso login --profile %s` first", c.cfg.Profile, left.Round(time.Minute), c.cfg.Profile))
		}
	}
	return warnings
}

// ssoSessionEnd is when the profile's SSO session ends, as far as anyone can
// tell. The expiry of a token with a refresh token is only the next refresh.
func (c *Client) ssoSessionEnd(ctx context.Context) (time.Time, bool) {
	if c.cfg.Profile == "" {
		return time.Time{}, false
	}
	profile, err := config.LoadSharedConfigProfile(ctx, c.cfg.Profile, func(o *config.LoadSharedConfigOptions) {
		if f := os.Getenv("AWS_CONFIG_FILE"); f != "" {
			o.ConfigFiles = []string{f}
		}
		if f := os.Getenv("AWS_SHARED_CREDENTIALS_FILE"); f != "" {
			o.CredentialsFiles = []string{f}
		}
	})
	if err != nil {
		return time.Time{}, false
	}
	key := cmp.Or(profile.SSOSessionName, profile.SSOStartURL)
	if key == "" {
		return time.Time{}, false
	}
	file, err := ssocreds.StandardCachedTokenFilepath(key)
	if err != nil {
		return time.Time{}, false
	}
	data, err := os.ReadFile(file) //nolint:gosec // the AWS tools' own cache
	if err != nil {
		return time.Time{}, false
	}
	var token struct {
		ExpiresAt    time.Time `json:"expiresAt"`
		RefreshToken string    `json:"refreshToken"`
	}
	if json.Unmarshal(data, &token) != nil || token.RefreshToken != "" {
		return time.Time{}, false
	}
	return token.ExpiresAt, true
}

// mint gets new credentials, warning once if they can do more than Bedrock.
func (k *Keeper) mint(ctx context.Context) (aws.Credentials, error) {
	c := k.client
	duration := aws.Int32(int32(lifetime / time.Second))
	if c.cfg.RoleARN != "" {
		out, err := c.sts.AssumeRole(ctx, &sts.AssumeRoleInput{
			RoleArn: aws.String(c.cfg.RoleARN), RoleSessionName: aws.String(sessionName),
			Policy: aws.String(policy), DurationSeconds: duration,
		})
		if err != nil {
			return aws.Credentials{}, fmt.Errorf("AWS STS AssumeRole of %s: %w", c.cfg.RoleARN, err)
		}
		return fromSTS(out.Credentials)
	}
	if c.cfg.PassThrough {
		// The SDK's cache would answer with what it holds until that has
		// expired, which is too late to rewrite the file.
		if cache, ok := c.source.(interface{ Invalidate() }); ok {
			cache.Invalidate()
		}
		creds, err := c.source.Retrieve(ctx)
		if err != nil {
			return aws.Credentials{}, fmt.Errorf("reading your AWS credentials: %w", err)
		}
		return creds, nil
	}
	federated, err := c.sts.GetFederationToken(ctx, &sts.GetFederationTokenInput{
		Name: aws.String(sessionName), Policy: aws.String(policy), DurationSeconds: duration,
	})
	if err == nil {
		return fromSTS(federated.Credentials)
	}
	// Only a refusal falls back: a token that can do more than Bedrock is no
	// answer to a network problem.
	if !refused(err) {
		return aws.Credentials{}, fmt.Errorf("AWS STS GetFederationToken: %w", err)
	}
	session, err := c.sts.GetSessionToken(ctx, &sts.GetSessionTokenInput{DurationSeconds: duration})
	if refused(err) {
		return aws.Credentials{}, fmt.Errorf("AWS refused both GetFederationToken and GetSessionToken (temporary credentials, such as an SSO profile's, can call neither): %w; set `role_arn` to a role they may assume, or set `i_accept_long_lived_keys_in_sandbox = true` in .fliesswerk/config.local.toml", err)
	}
	if err != nil {
		return aws.Credentials{}, fmt.Errorf("AWS STS GetSessionToken: %w", err)
	}
	if !k.warned {
		k.warned = true
		k.d.Warn("AWS refused GetFederationToken, so the Sandbox gets a session token that is not limited to Bedrock; allow sts:GetFederationToken, or set `role_arn`")
	}
	return fromSTS(session.Credentials)
}

// refused reports whether AWS said no to the call, as opposed to failing it.
func refused(err error) bool {
	var api smithy.APIError
	return errors.As(err, &api) && strings.Contains(api.ErrorCode(), "AccessDenied")
}

func fromSTS(c *types.Credentials) (aws.Credentials, error) {
	if c == nil {
		return aws.Credentials{}, errors.New("AWS STS returned no credentials")
	}
	return aws.Credentials{
		AccessKeyID:     aws.ToString(c.AccessKeyId),
		SecretAccessKey: aws.ToString(c.SecretAccessKey),
		SessionToken:    aws.ToString(c.SessionToken),
		CanExpire:       true,
		Expires:         aws.ToTime(c.Expiration),
	}, nil
}

// export is the file: what `aws sts` prints, which Claude Code reads.
type export struct {
	Credentials exportedCredentials `json:"Credentials"`
}

type exportedCredentials struct {
	AccessKeyID     string `json:"AccessKeyId"`
	SecretAccessKey string `json:"SecretAccessKey"`
	SessionToken    string `json:"SessionToken"`
	Expiration      string `json:"Expiration,omitempty"`
}

// write tells the redactor about creds, then replaces the file in one step:
// a reader sees the old credentials or the new ones, never half of either.
func (k *Keeper) write(creds aws.Credentials) error {
	k.tellRedactor(creds)
	out := exportedCredentials{AccessKeyID: creds.AccessKeyID, SecretAccessKey: creds.SecretAccessKey, SessionToken: creds.SessionToken}
	if creds.CanExpire {
		out.Expiration = creds.Expires.UTC().Format(time.RFC3339)
	}
	data, err := json.Marshal(export{out})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(k.d.Dir, ".new-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, err = tmp.Write(data)
	if err := errors.Join(err, tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), k.File())
}

func (k *Keeper) tellRedactor(creds aws.Credentials) {
	for _, s := range []string{creds.AccessKeyID, creds.SecretAccessKey, creds.SessionToken} {
		k.d.Secret(s)
	}
}
