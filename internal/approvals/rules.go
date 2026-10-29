package approvals

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Rule is one critical rule: a call whose command matches Pattern (RE2,
// unanchored) is critical. Built-in rules keep their pattern from the
// code, so a fix ships with an upgrade; only Enabled is the owner's.
type Rule struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
	Enabled bool   `json:"enabled"`
	Builtin bool   `json:"builtin"`
}

// Limits on owner rules.
const (
	maxCustomRules = 50
	maxPattern     = 500
	maxRuleName    = 80
)

var ruleIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// at is a command position: the start, a shell operator, a subshell or
// a backtick, or a word that runs the next one (sudo, exec, ...). A
// command word mentioned as an argument ("echo shutdown") is not one.
const at = "(?:^|[;&|()`]|\\b(?:sudo|doas|exec|nohup|xargs|env|time)\\s)\\s*"

// DefaultRules are PLAN.md §4's critical steps. They are a tripwire for
// honest agents, not a sandbox: a hostile one can always spell a
// command another way.
func DefaultRules() []Rule {
	rules := []Rule{
		{ID: "recursive-delete", Name: "Recursive delete", Pattern: `\brm\b[^;&|\n]*\s(?:-[a-zA-Z]*[rR][a-zA-Z]*|--recursive)\b` +
			`|(?i:\b(?:Remove-Item|ri|rmdir|rd|del|erase)\b[^;&|\n]*\s(?:-Recurse\b|/s\b))` +
			`|\bfind\b[^;&|\n]*\s-delete\b`},
		{ID: "disk-format", Name: "Disk format", Pattern: at + `(?:mkfs(?:\.\w+)?|wipefs|(?i:Format-Volume|Clear-Disk))(?:\s|$)` +
			`|\bdiskutil\s+(?:erase\w*|reformat|partitionDisk)\b|\bsgdisk\b[^;&|\n]*\s--zap` +
			`|` + at + `(?i:format(?:\.com)?)\s+[A-Za-z]:`},
		{ID: "dd-of", Name: "dd to a device or file", Pattern: `\bdd\b[^;&|\n]*\bof=`},
		{ID: "shutdown", Name: "Shutdown or reboot", Pattern: at + `(?:shutdown|reboot|halt|poweroff)(?:\s|$|;)` +
			`|\binit\s+[06]\b|\bsystemctl\s+(?:reboot|poweroff|halt|kexec)\b|(?i:\b(?:Restart|Stop)-Computer\b)`},
		{ID: "force-push", Name: "Force push", Pattern: `\bgit\b[^;&|\n]*\spush\b[^;&|\n]*(?:\s--force(?:-with-lease|-if-includes)?\b|\s-[a-zA-Z]*f[a-zA-Z]*\b|\s\+\S)`},
		{ID: "git-reset-hard", Name: "git reset --hard", Pattern: `\bgit\b[^;&|\n]*\sreset\b[^;&|\n]*\s--hard\b`},
		{ID: "pipe-to-shell", Name: "Download piped to a shell", Pattern: `(?i)\b(?:curl|wget|iwr|irm|Invoke-WebRequest|Invoke-RestMethod)\b[^;&\n]*\|\s*(?:sudo\s+)?(?:(?:ba|z|da|k|fi)?sh|python3?|perl|ruby|node|iex|Invoke-Expression)\b` +
			`|(?i)\b(?:ba|z|da|k)?sh\b[^;&\n]*(?:<\(|\$\()\s*(?:curl|wget)\b`},
		{ID: "recursive-chmod", Name: "Recursive chmod or chown", Pattern: `\b(?:chmod|chown|chgrp)\b[^;&|\n]*\s(?:-[a-zA-Z]*R[a-zA-Z]*|--recursive)\b` +
			`|(?i:\bicacls\b[^;&|\n]*\s/t\b|\btakeown\b[^;&|\n]*\s/r\b)`},
		{ID: "drop-table", Name: "DROP TABLE", Pattern: `(?i)\b(?:drop\s+(?:table|database|schema)|truncate\s+table)\b`},
		{ID: "crontab-remove", Name: "crontab -r", Pattern: `\bcrontab\b[^;&|\n]*\s-[a-zA-Z]*r[a-zA-Z]*\b`},
		// Last: a more specific rule above names the step better.
		{ID: "elevate", Name: "sudo, su or runas", Pattern: at + `(?:sudo|doas|pkexec|(?i:runas))(?:\s|$)|` + at + `su(?:\s+-|\s+root\b|\s*$)`},
	}
	for i := range rules {
		rules[i].Enabled, rules[i].Builtin = true, true
	}
	return rules
}

// ruleSet is a compiled list.
type ruleSet struct {
	rules []Rule
	res   []*regexp.Regexp
}

func compile(rules []Rule) (ruleSet, error) {
	rs := ruleSet{rules: slices.Clone(rules)}
	for _, r := range rules {
		re, err := regexp.Compile(r.Pattern)
		if err != nil {
			return ruleSet{}, fmt.Errorf("approvals: rule %s: pattern does not compile: %v", r.ID, err)
		}
		rs.res = append(rs.res, re)
	}
	return rs, nil
}

// match returns the first enabled rule the command matches.
func (rs ruleSet) match(command string) (Rule, bool) {
	for i, r := range rs.rules {
		if r.Enabled && rs.res[i].MatchString(command) {
			return r, true
		}
	}
	return Rule{}, false
}

// merge applies saved (the owner's list) to the built-ins: a built-in
// keeps its code pattern and name and takes the saved Enabled (true
// when not saved, so a rule added by an upgrade starts on); custom
// rules follow in saved order. It validates the result.
func merge(saved []Rule) ([]Rule, error) {
	builtins := DefaultRules()
	enabled := map[string]bool{}
	var custom []Rule
	seen := map[string]bool{}
	for _, r := range saved {
		if seen[r.ID] {
			return nil, fmt.Errorf("approvals: rule id %q is used twice", r.ID)
		}
		seen[r.ID] = true
		if slices.ContainsFunc(builtins, func(b Rule) bool { return b.ID == r.ID }) {
			enabled[r.ID] = r.Enabled
			continue
		}
		if r.Builtin {
			// A built-in a later version dropped: let it go.
			continue
		}
		r.Name = strings.TrimSpace(r.Name)
		switch {
		case !ruleIDRE.MatchString(r.ID):
			return nil, fmt.Errorf("approvals: rule id %q must be 1 to 40 of a-z 0-9 -, starting with a letter or digit", r.ID)
		case r.Name == "" || len(r.Name) > maxRuleName:
			return nil, fmt.Errorf("approvals: rule %s needs a name of up to %d bytes", r.ID, maxRuleName)
		case r.Pattern == "" || len(r.Pattern) > maxPattern:
			return nil, fmt.Errorf("approvals: rule %s needs a pattern of up to %d bytes", r.ID, maxPattern)
		}
		if _, err := regexp.Compile(r.Pattern); err != nil {
			return nil, fmt.Errorf("approvals: rule %s: pattern does not compile: %v", r.ID, err)
		}
		custom = append(custom, r)
	}
	if len(custom) > maxCustomRules {
		return nil, fmt.Errorf("approvals: at most %d rules of your own", maxCustomRules)
	}
	out := make([]Rule, 0, len(builtins)+len(custom))
	for _, b := range builtins {
		if e, ok := enabled[b.ID]; ok {
			b.Enabled = e
		}
		out = append(out, b)
	}
	return append(out, custom...), nil
}
