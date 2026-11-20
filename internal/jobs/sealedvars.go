// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package jobs

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/shrink-your-shit/shrink-your-shit/internal/workflow"
)

// A workflow's secret variable (W4b QA step 3): its value is stored sealed wherever the workflow doc is
// (the draft and every revision), so the database and its backups hold no secret in the clear. It is
// opened in one place, RunInfoFor, which is what a job resolves its workflow's variables with.

// sealedVar is a secret variable's value as the database holds it, sealed with this computer's share key.
// It is the value a client would see only if it were not masked: the API never sends a secret's value.
// The fields are declared in key order (nonce before sealed), because a doc read back from the database
// holds a map and its JSON comes out sorted: this way a doc written just after sealing marshals to the
// same bytes as the same doc read back, so its logic hash does not change across a save (draftDiffers).
type sealedVar struct {
	Nonce  []byte `json:"nonce"`
	Sealed []byte `json:"sealed"`
}

// sealedOf reads v as a sealed secret's value. A map is what a doc read back from the database has (a
// stored doc's JSON is decoded into any); the struct is what a save that just sealed it holds.
func sealedOf(v any) (ct, nonce []byte, ok bool) {
	switch m := v.(type) {
	case sealedVar:
		return m.Sealed, m.Nonce, true
	case map[string]any:
		s, sok := m["sealed"].(string)
		n, nok := m["nonce"].(string)
		if !sok || !nok {
			return nil, nil, false
		}
		ct, cerr := base64.StdEncoding.DecodeString(s)
		nonce, nerr := base64.StdEncoding.DecodeString(n)
		return ct, nonce, cerr == nil && nerr == nil && len(ct) > 0 && len(nonce) > 0
	}
	return nil, nil, false
}

// sealDoc is sealDocWith with no shared memory of what it sealed.
func sealDoc(doc workflow.Doc) (workflow.Doc, error) {
	out, _, err := sealDocWith(doc, nil)
	return out, err
}

// sealDocWith replaces every plain secret variable's value in doc with its sealed form, and says whether it
// changed the doc. A value that is already sealed is left alone: a page is never sent a secret, so saving it
// back must not seal (or lose) the one stored. memo, when given, remembers what it sealed for a plaintext,
// so one value that appears in several docs gets one sealed form (a draft and its published revision stay
// equal, as they were).
func sealDocWith(doc workflow.Doc, memo map[string]sealedVar) (workflow.Doc, bool, error) {
	var out *workflow.Doc
	for i, v := range doc.Variables {
		if v.Type != "secret" || v.Value == nil {
			continue
		}
		if _, _, ok := sealedOf(v.Value); ok {
			continue
		}
		plain, ok := v.Value.(string)
		if !ok {
			// a secret is text, and only text can be sealed: an older build or another client may have stored
			// another shape (an object, a number), so seal its JSON form rather than leave it in the clear
			raw, err := json.Marshal(v.Value)
			if err != nil {
				return doc, false, err
			}
			plain = string(raw)
		}
		if plain == "" { // nothing to seal, and SealSecret would store an empty one as sealed
			continue
		}
		sv, seen := memo[plain]
		if !seen {
			ct, nonce, err := SealSecret(plain)
			if err != nil {
				return doc, false, err
			}
			sv = sealedVar{Sealed: ct, Nonce: nonce}
			if memo != nil {
				memo[plain] = sv
			}
		}
		if out == nil {
			d := doc
			d.Variables = append([]workflow.Variable(nil), doc.Variables...)
			out = &d
		}
		out.Variables[i].Value = sv
	}
	if out == nil {
		return doc, false, nil
	}
	return *out, true, nil
}

// openVariables gives a stored doc's secret values in the clear, and the names of the ones this computer's
// share key cannot open. The caller must treat a name it gets back as a failure: a secret read as an empty
// string would let the job run with the wrong value.
func openVariables(doc workflow.Doc) ([]workflow.Variable, []string) {
	var out []workflow.Variable
	var unreadable []string
	for i, v := range doc.Variables {
		if v.Type != "secret" {
			continue
		}
		ct, nonce, ok := sealedOf(v.Value)
		if !ok {
			continue
		}
		plain, ok := openSecretHere(ct, nonce)
		if ok {
			if out == nil {
				out = append([]workflow.Variable(nil), doc.Variables...)
			}
			out[i].Value = plain
			continue
		}
		unreadable = append(unreadable, v.Name)
	}
	if out == nil {
		return doc.Variables, unreadable
	}
	return out, unreadable
}

// SecretUnreadableError names the workflow's secrets this computer can't open: they were sealed with another
// machine's share key (a backup restored here from there). A job fails on it rather than run without them.
type SecretUnreadableError struct{ Names []string }

func (e *SecretUnreadableError) Error() string {
	quoted := make([]string, len(e.Names))
	for i, n := range e.Names {
		quoted[i] = "“" + n + "”"
	}
	what := "the secret " + strings.Join(quoted, ", ")
	if len(e.Names) > 1 {
		what = "the secrets " + strings.Join(quoted, ", ")
	}
	return what + " in this workflow can't be opened on this computer: its share key is different from the one it was sealed with"
}

// rowQuerier is what the sealing pass needs: the store's own connection, or a restore's transaction.
type rowQuerier interface {
	Query(string, ...any) (*sql.Rows, error)
	Exec(string, ...any) (sql.Result, error)
}

// sealStoredVariables seals the secret values in every stored workflow doc: a draft or revision saved by a
// build before values were sealed holds them in the clear. It runs when the store opens, as EnsureRevisions
// does, and before a restore commits, so no plaintext secret is left behind. One plaintext value gets the
// same sealed form in every doc it appears in, so docs that were equal stay equal (see sealDocWith).
func sealStoredVariables(q rowQuerier) error {
	rows, err := q.Query(`SELECT id, doc FROM workflow_revisions`)
	if err != nil {
		return err
	}
	type change struct {
		id  int64
		doc workflow.Doc
	}
	var todo []change
	memo := map[string]sealedVar{}
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		var d workflow.Doc
		if json.Unmarshal([]byte(raw), &d) != nil { // not a doc this build reads: left as it is
			continue
		}
		sd, changed, err := sealDocWith(d, memo)
		if err != nil {
			rows.Close()
			return err
		}
		if changed {
			todo = append(todo, change{id, sd})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range todo {
		if _, err := q.Exec(`UPDATE workflow_revisions SET doc=?, logic_hash=? WHERE id=?`,
			mustJSON(c.doc), workflow.LogicHash(c.doc), c.id); err != nil {
			return err
		}
	}
	return nil
}
