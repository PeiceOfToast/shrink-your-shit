// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package share

import (
	"errors"
	"strings"
	"testing"
)

// The forms a person types. Each normalizes to one source and one subfolder, so the helper always
// runs the same mount for the same share.
func TestParseAddressReadsEveryForm(t *testing.T) {
	cases := []struct {
		raw    string
		hint   string
		typ    Type
		source string
		sub    string
		shown  string
		name   string
	}{
		{raw: `\\nas\media`, typ: SMB, source: "//nas/media", shown: "//nas/media", name: "media"},
		{raw: `\\nas\media\Films`, typ: SMB, source: "//nas/media", sub: "Films", shown: "//nas/media/Films", name: "Films"},
		{raw: "//nas/media", typ: SMB, source: "//nas/media", shown: "//nas/media", name: "media"},
		{raw: "//nas/media/Films", typ: SMB, source: "//nas/media", sub: "Films", shown: "//nas/media/Films", name: "Films"},
		{raw: "smb://nas/media/Films", typ: SMB, source: "//nas/media", sub: "Films", shown: "//nas/media/Films", name: "Films"},
		{raw: "//192.168.1.10/media", typ: SMB, source: "//192.168.1.10/media", shown: "//192.168.1.10/media", name: "media"},
		{raw: "nas:/export", typ: NFS, source: "nas:/export", shown: "nas:/export", name: "export"},
		{raw: "nas:/export/Films", typ: NFS, source: "nas:/export", sub: "Films", shown: "nas:/export/Films", name: "Films"},
		{raw: "nfs://nas:/export/Films", typ: NFS, source: "nas:/export", sub: "Films", shown: "nas:/export/Films", name: "Films"},
		{raw: "nfs://nas/export/Films", typ: NFS, source: "nas:/export", sub: "Films", shown: "nas:/export/Films", name: "Films"},
		{raw: "  //nas/media/Films/  ", typ: SMB, source: "//nas/media", sub: "Films", shown: "//nas/media/Films", name: "Films"},
		// A hint that agrees is fine; the address still decides.
		{raw: "//nas/media", hint: "smb", typ: SMB, source: "//nas/media", shown: "//nas/media", name: "media"},
		{raw: "nas:/export", hint: "NFS", typ: NFS, source: "nas:/export", shown: "nas:/export", name: "export"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			a, err := ParseAddress(tc.raw, tc.hint)
			if err != nil {
				t.Fatal(err)
			}
			if a.Type != tc.typ || a.Source != tc.source || a.Sub != tc.sub {
				t.Errorf("address = %+v, want %s %s %q", a, tc.typ, tc.source, tc.sub)
			}
			if got := a.String(); got != tc.shown {
				t.Errorf("String = %q, want %q", got, tc.shown)
			}
			if got := a.DefaultName(); got != tc.name {
				t.Errorf("DefaultName = %q, want %q", got, tc.name)
			}
		})
	}
}

// A wrong or dangerous address is refused before anything reaches the helper. The refusals carry
// the API's code and field, so the form can point at the input.
func TestParseAddressRefuses(t *testing.T) {
	cases := []struct {
		name  string
		raw   string
		hint  string
		code  string
		field string
	}{
		{"empty", "  ", "", "invalid_address", "address"},
		{"no shape", "nas/media", "", "invalid_address", "address"},
		{"host only", "//nas", "", "invalid_address", "address"},
		{"ipv6 brackets", "//[::1]/media", "", "invalid_address", "address"},
		{"leading dash host", "//-nas/media", "", "invalid_address", "address"},
		{"dot-dot sub", "//nas/media/../etc", "", "invalid_address", "address"},
		{"dot sub", "//nas/media/.", "", "invalid_address", "address"},
		{"control character", "//nas/me\ndia", "", "invalid_address", "address"},
		{"smb given as nfs", "//nas/media", "nfs", "invalid_address", "address"},
		{"nfs given as smb", "nas:/export", "smb", "invalid_address", "address"},
		{"unknown type", "//nas/media", "ftp", "invalid_type", "type"},
		{"nfs no path", "nas:", "", "invalid_address", "address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseAddress(tc.raw, tc.hint)
			if err == nil {
				t.Fatal("the address was accepted")
			}
			var invalid *InvalidError
			if !errors.As(err, &invalid) {
				t.Fatalf("error = %v, want an InvalidError", err)
			}
			if invalid.Code != tc.code || invalid.Field != tc.field || invalid.Message == "" {
				t.Errorf("error = %+v, want %s on %s with a sentence", invalid, tc.code, tc.field)
			}
		})
	}
}

// SafeOptions is the floor under every mount, and NFS's soft timeout is the one that keeps a
// dropped server from blocking a job forever (question 5).
func TestSafeOptions(t *testing.T) {
	smb := strings.Join(SafeOptions(SMB), ",")
	if smb != "nosuid,nodev,noexec" {
		t.Errorf("SMB options = %q", smb)
	}
	nfs := strings.Join(SafeOptions(NFS), ",")
	if nfs != "nosuid,nodev,noexec,soft,timeo=100,retrans=2" {
		t.Errorf("NFS options = %q", nfs)
	}
}

// The helper rebuilds options from this list; anything outside it is refused, and a password can
// never be an option (it goes in a 0600 credentials file instead).
func TestCheckOptions(t *testing.T) {
	good := []struct {
		typ  Type
		opts []string
	}{
		{SMB, []string{"vers=3.1.1", "guest", "domain=WORKGROUP"}},
		{SMB, []string{"vers=2.0", "vers=2.1", "vers=3.0", "vers=3.02"}},
		{NFS, []string{"vers=3", "vers=4", "vers=4.1", "vers=4.2", "tcp", "soft", "timeo=50", "retrans=3"}},
	}
	for _, tc := range good {
		if err := CheckOptions(tc.typ, tc.opts); err != nil {
			t.Errorf("%s %v refused: %v", tc.typ, tc.opts, err)
		}
	}
	bad := []struct {
		typ Type
		opt string
	}{
		{SMB, "password=hunter2"},
		{SMB, "credentials=/tmp/x"},
		{SMB, "vers=1.0"},
		{SMB, "uid=0"},
		{SMB, "nosuid,nodev"},
		{SMB, " vers=3.0"},
		{NFS, "hard"},
		{NFS, "vers=2"},
		{NFS, "timeo=0"},
		{NFS, "retrans=999"},
		{NFS, "password=x"},
		// A comma would split one option into two once the helper joins them (review of #921).
		{SMB, "domain=x,password=y"},
		{NFS, "soft,password=y"},
	}
	for _, tc := range bad {
		if err := CheckOptions(tc.typ, []string{tc.opt}); err == nil {
			t.Errorf("%s option %q was accepted", tc.typ, tc.opt)
		}
	}
}

// A comma refusal never echoes the option, since a password can hide after the comma.
func TestCheckOptionsCommaRefusalEchoesNothing(t *testing.T) {
	err := CheckOptions(SMB, []string{"domain=x,password=s3cret"})
	if err == nil {
		t.Fatal("a comma option was accepted")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Errorf("the refusal echoes the hidden password: %q", err)
	}
}

// An unknown option's refusal names it but never echoes its value.
func TestCheckOptionsUnknownRefusalEchoesNoValue(t *testing.T) {
	err := CheckOptions(SMB, []string{"pass=s3cret"})
	if err == nil {
		t.Fatal("an unknown option was accepted")
	}
	if strings.Contains(err.Error(), "s3cret") {
		t.Errorf("the refusal echoes the value: %q", err)
	}
}

// A password as an option is refused by its name, and the refusal never echoes the value.
func TestCheckOptionsRefusesAPasswordByName(t *testing.T) {
	err := CheckOptions(SMB, []string{"password=hunter2"})
	if err == nil {
		t.Fatal("password=hunter2 was accepted")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the refusal echoes the password: %q", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "password") {
		t.Errorf("the refusal doesn't say why: %q", err)
	}
}
