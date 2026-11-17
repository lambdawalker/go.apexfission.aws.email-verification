package main

import (
	"encoding/json"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
)

type hostConfig struct {
	Version                   int
	Sender, Recipient, Domain string
}

func hostSettings(dir string, ci, reconfigure bool, sender, recipient, domain *string) error {
	path := filepath.Join(dir, "testhost.json")
	saved := hostConfig{Version: 1}
	if raw, e := os.ReadFile(path); e == nil {
		if json.Unmarshal(raw, &saved) != nil || saved.Version != 1 {
			return fmt.Errorf("invalid saved host settings")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	for _, v := range []struct {
		name string
		ptr  *string
		old  string
	}{{"Sender", sender, saved.Sender}, {"Recipient", recipient, saved.Recipient}, {"Ownership domain", domain, saved.Domain}} {
		if *v.ptr == "" && !reconfigure {
			*v.ptr = v.old
		}
		if *v.ptr == "" {
			if ci {
				return fmt.Errorf("explicit %s required", v.name)
			}
			*v.ptr = prompt(v.name, "")
		}
	}
	for _, value := range []string{*sender, *recipient} {
		a, e := mail.ParseAddress(value)
		if e != nil || a.Address != value {
			return fmt.Errorf("plain sender/recipient email required")
		}
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*[a-z0-9]$`).MatchString(*domain) {
		return fmt.Errorf("valid lowercase ownership domain required")
	}
	raw, _ := json.MarshalIndent(hostConfig{1, *sender, *recipient, *domain}, "", "  ")
	return atomicWrite(path, raw)
}
