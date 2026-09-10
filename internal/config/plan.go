package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// PlanAuto asks tallybook to work out the plan from how Claude Code is
// logged in. It is the default.
const PlanAuto Plan = "auto"

// DetectPlan decides between the API plan (dollars are a bill) and a
// subscription (dollars are a list-price equivalent, share of usage is the
// real currency). It reads only non-secret account fields from
// ~/.claude.json. The second value says what the decision was based on.
func DetectPlan() (Plan, string) {
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return PlanAPI, "ANTHROPIC_API_KEY is set"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return PlanAPI, "no home directory; assuming API"
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return PlanAPI, "no ~/.claude.json; assuming API"
	}
	var doc struct {
		OAuthAccount struct {
			BillingType      string `json:"billingType"`
			OrganizationType string `json:"organizationType"`
		} `json:"oauthAccount"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return PlanAPI, "could not read ~/.claude.json; assuming API"
	}
	bt := strings.ToLower(doc.OAuthAccount.BillingType)
	ot := strings.ToLower(doc.OAuthAccount.OrganizationType)
	switch {
	case strings.Contains(bt, "subscription"), strings.HasPrefix(ot, "claude_max"), strings.HasPrefix(ot, "claude_pro"):
		return PlanSubscription, "Claude Code is logged in with a " + strings.TrimPrefix(ot, "claude_") + " subscription"
	case bt != "" || ot != "":
		return PlanAPI, "Claude Code account billing type is " + bt
	}
	return PlanAPI, "no subscription found; assuming API"
}

// Resolve turns PlanAuto into a concrete plan.
func (c *Config) Resolve() (Plan, string) {
	if c.Plan != PlanAuto {
		return c.Plan, "set in config"
	}
	return DetectPlan()
}
