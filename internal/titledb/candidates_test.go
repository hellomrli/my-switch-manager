package titledb

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"my-switch-manager/internal/nsw"
)

func candidateDB(t *testing.T, cnmts string) *DB {
	t.Helper()
	dir := t.TempDir()
	for name, text := range map[string]string{"versions.json": "{}", "cnmts.json": cnmts} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestNCACandidatesDeterministic(t *testing.T) {
	const fixture = `{"0100aaaa00000800":{
 "old":{"titleType":129,"version":65536,"contentEntries":[{"ncaId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{"ncaId":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]},
 "new":{"titleType":129,"version":"131072","contentEntries":[{"ncaId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{"ncaId":"cccccccccccccccccccccccccccccccc"}]},
 "duplicate":{"titleType":129,"version":65536,"contentEntries":[{"ncaId":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},{"ncaId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}
 },"0100aaaa00001001":{"0":{"titleType":130,"version":0,"contentEntries":[{"ncaId":"dddddddddddddddddddddddddddddddd"}]}}}`
	upd, _ := nsw.ParseTitleID("0100AAAA00000800")
	dlc, _ := nsw.ParseTitleID("0100AAAA00001001")
	a, b, c, d := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32), strings.Repeat("d", 32)
	for i := 0; i < 30; i++ {
		db := candidateDB(t, fixture)
		want := []nsw.Content{{TitleID: upd, Version: 65536}, {TitleID: upd, Version: 131072}}
		if got := db.LookupNCACandidates(a); !reflect.DeepEqual(got, want) {
			t.Fatalf("candidates: %+v", got)
		}
		if _, ok := db.LookupNCA(a); ok {
			t.Fatal("shared NCA must not have a single owner")
		}
		copy := db.LookupNCACandidates(a)
		copy[0].Version = 999
		if got := db.LookupNCACandidates(a); !reflect.DeepEqual(got, want) {
			t.Fatal("caller changed index")
		}
		for _, tc := range []struct {
			ids  []string
			want []nsw.Content
		}{
			{[]string{a, b}, []nsw.Content{{TitleID: upd, Version: 65536}}},
			{[]string{c, a, a}, []nsw.Content{{TitleID: upd, Version: 131072}}},
			{[]string{a}, []nsw.Content{{TitleID: upd, Version: -1}}},
			{[]string{b}, []nsw.Content{{TitleID: upd, Version: -1}}},
			{[]string{a, b, c}, []nsw.Content{{TitleID: upd, Version: -1}}},
			{[]string{d, a, b}, []nsw.Content{{TitleID: upd, Version: 65536}, {TitleID: dlc, Version: 0}}},
		} {
			if got := db.MatchNCAs(tc.ids); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%v: got %+v want %+v", tc.ids, got, tc.want)
			}
		}
	}
}

func TestNCAMissingOrInvalidRequirements(t *testing.T) {
	a := strings.Repeat("a", 32)
	for name, versions := range map[string]string{
		"identical":          `"1":{"version":1,"contentEntries":[{"ncaId":"` + a + `"}]},"2":{"version":2,"contentEntries":[{"ncaId":"` + a + `"}]}`,
		"malformed-id":       `"1":{"version":1,"contentEntries":[{"ncaId":"` + a + `"},{"ncaId":"bad"}]}`,
		"missing-version":    `"1":{"contentEntries":[{"ncaId":"` + a + `"}]}`,
		"negative-version":   `"1":{"version":-1,"contentEntries":[{"ncaId":"` + a + `"}]}`,
		"fractional-version": `"1":{"version":1.5,"contentEntries":[{"ncaId":"` + a + `"}]}`,
		"overflow-version":   `"1":{"version":"4294967296","contentEntries":[{"ncaId":"` + a + `"}]}`,
		"conflicting-lists":  `"1":{"version":1,"contentEntries":[{"ncaId":"` + a + `"}]},"other":{"version":1,"contentEntries":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			db := candidateDB(t, `{"0100aaaa00000800":{`+versions+`}}`)
			got := db.MatchNCAs([]string{a})
			if len(got) != 1 || got[0].Version != -1 {
				t.Fatalf("unsafe resolution: %+v", got)
			}
		})
	}
}
