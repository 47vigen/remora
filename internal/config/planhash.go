package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// PlanHash binds a plan to both what it would write and what it was planned against: the hex sha256 of the
// canonical JSON of {prune, want (normalised), secrets (name → sha256 of the value, never the value), current (the
// store exported as a File, normalised)}. A different document, secret or prune flag, or any store change since
// planning, gives a different hash.
func PlanHash(prune bool, want File, sec Secrets, current File) string {
	sum := sha256.Sum256(planCanon(prune, want, sec, current))
	return hex.EncodeToString(sum[:])
}

func planCanon(prune bool, want File, sec Secrets, current File) []byte {
	digests := make(map[string]string, len(sec))
	for name, v := range sec {
		sum := sha256.Sum256([]byte(v))
		digests[name] = hex.EncodeToString(sum[:])
	}
	b, err := json.Marshal(struct {
		Prune   bool              `json:"prune"`
		Want    File              `json:"want"`
		Secrets map[string]string `json:"secrets"`
		Current File              `json:"current"`
	}{prune, normalise(want), digests, normalise(current)})
	if err != nil {
		panic(err) // File holds only strings, ints, bools, slices and string maps
	}
	return b
}
