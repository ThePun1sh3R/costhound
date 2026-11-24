package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/organizations"
	orgtypes "github.com/aws/aws-sdk-go-v2/service/organizations/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/ThePun1sh3R/costhound/internal/inventory"
)

const buySite = "https://costhound.dev"

// buy prints a costhound.dev purchase link with plan, term and account IDs
// already filled in: `costhound buy [-plan pro|team] [-term month|year] [-profile P] [-org]`.
// Free builds include it so buyers don't retype account IDs.
func buy(args []string) int {
	return buyTo(args, os.Stdout, os.Stderr, resolveBuyAccounts)
}

// buyAccounts looks up the AWS account ID(s) for a purchase link.
type buyAccounts func(ctx context.Context, profile string, org bool) ([]string, error)

func buyTo(args []string, stdout, stderr io.Writer, lookup buyAccounts) int {
	fs := flag.NewFlagSet("costhound buy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "costhound buy: print a purchase link with this account's ID already filled in.\n\nUsage: costhound buy [-plan pro|team] [-term month|year] [-profile P] [-org]\n\nFlags:\n")
		fs.PrintDefaults()
	}
	plan := fs.String("plan", "pro", "pro or team")
	term := fs.String("term", "month", "month or year")
	profile := fs.String("profile", "", "AWS profile to use (default: the usual AWS credential chain)")
	org := fs.Bool("org", false, "include every active account of the AWS Organization (for Team)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if err := checkBuyPlan(*plan); err != nil {
		fmt.Fprintf(stderr, "costhound: %v\n", err)
		return 2
	}
	if err := checkBuyTerm(*term); err != nil {
		fmt.Fprintf(stderr, "costhound: %v\n", err)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	accounts, err := lookup(ctx, *profile, *org)
	if err != nil {
		msg := err.Error()
		if strings.HasPrefix(msg, "costhound:") {
			fmt.Fprintln(stderr, msg)
		} else {
			fmt.Fprintf(stderr, "costhound: %s\n", msg)
		}
		fmt.Fprintln(stdout, buySite)
		return 0
	}
	fmt.Fprintln(stdout, buyLink(*plan, *term, accounts))
	fmt.Fprintln(stdout, "Account IDs are in the URL fragment (#); browsers never send that part to a server.")
	return 0
}

func checkBuyPlan(plan string) error {
	switch plan {
	case "pro", "team":
		return nil
	}
	return fmt.Errorf("-plan is pro or team, not %q", plan)
}

func checkBuyTerm(term string) error {
	switch term {
	case "month", "year":
		return nil
	}
	return fmt.Errorf("-term is month or year, not %q", term)
}

// buyLink is https://costhound.dev/#buy=<plan>,<term>,<acct>[+<acct>...].
func buyLink(plan, term string, accounts []string) string {
	return buySite + "/#buy=" + plan + "," + term + "," + strings.Join(accounts, "+")
}

// resolveBuyAccounts reads the caller's account from STS, or every active
// Organization member account when org is set (organizations:ListAccounts).
func resolveBuyAccounts(ctx context.Context, profile string, org bool) ([]string, error) {
	opts := []func(*config.LoadOptions) error{config.WithRetryMaxAttempts(3)}
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, errors.New(configHelp(err))
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	who, err := sts.NewFromConfig(cfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, errors.New(loginHelp(err))
	}
	self := aws.ToString(who.Account)
	if self == "" {
		return nil, errors.New("STS GetCallerIdentity returned no account ID")
	}
	if !org {
		return []string{self}, nil
	}
	ids, err := listOrgAccountIDs(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, errors.New("the Organization has no active accounts")
	}
	return ids, nil
}

func listOrgAccountIDs(ctx context.Context, cfg aws.Config) ([]string, error) {
	orgs := organizations.NewFromConfig(cfg)
	var ids []string
	p := organizations.NewListAccountsPaginator(orgs, &organizations.ListAccountsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("can't list the Organization's accounts (%s); run -org from the management account or a delegated administrator account", inventory.ShortError(err))
		}
		for _, a := range page.Accounts {
			if a.Status != orgtypes.AccountStatusActive {
				continue
			}
			if id := aws.ToString(a.Id); id != "" {
				ids = append(ids, id)
			}
		}
	}
	sort.Strings(ids)
	return ids, nil
}
