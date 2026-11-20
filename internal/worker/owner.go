// Copyright (C) shrink-your-shit contributors. Licensed under AGPL-3.0-or-later.

package worker

// ownerOutcome is what giving the new file the source's owner and group came to (log-spec R14: the job log
// says what happened to the permissions and the owner).
type ownerOutcome int

const (
	ownerNothing ownerOutcome = iota // the system has no owner and group to copy (Windows)
	ownerKept                        // the owner and group are the source's
	ownerRefused                     // the system refused (not an administrator, or not in the source's group)
	ownerFailed                      // giving the file to the source's owner failed for another reason
)

// setOwner is keepOwner; tests make it refuse, as a worker that is not an administrator would.
var setOwner = keepOwner

// ownerWords is the job log's line for what setOwner came to: "" when the system has no owner to copy.
func ownerWords(o ownerOutcome, err error) string {
	switch o {
	case ownerKept:
		return "Kept the owner and group of the source file."
	case ownerRefused:
		return "Could not give the new file the source's owner and group: the system refused (the worker is not an administrator, or not in the source's group). The new file belongs to this worker's user instead. Run the worker as an administrator to keep the source's owner."
	case ownerFailed:
		return "Could not give the new file the source's owner and group: " + errWords(err) + ". The new file belongs to this worker's user instead."
	}
	return ""
}

// errWords is err's words, or "no error given" when it has none.
func errWords(err error) string {
	if err == nil {
		return "no error given"
	}
	return err.Error()
}
