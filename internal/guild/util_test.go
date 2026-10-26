package guild_test

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/mestre-ia/mestre/internal/protocol"
)

// decode and reply are the JSON of the handlers the tests write by hand.
func decode(w http.ResponseWriter, r *http.Request, into any) bool {
	if err := json.NewDecoder(r.Body).Decode(into); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// certOfEpoch is an unsigned certificate of epoch.
func certOfEpoch(guildID string, epoch uint64, citadel string, at time.Time) protocol.Certificate {
	return protocol.Certificate{
		GuildID: guildID, Epoch: epoch, CitadelMachineID: citadel, ApprovedBy: "dono", ApprovalID: "ap-1",
		IssuedAt: protocol.Timestamp{Time: at.UTC()},
	}
}
