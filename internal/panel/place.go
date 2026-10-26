package panel

import (
	"net/http"
)

// Place is where this machine stands for the panel.
type Place int

// The places.
const (
	// PlaceCitadel: the acting Citadel, which serves the panel.
	PlaceCitadel Place = iota
	// PlaceTower: any machine that is not the Citadel.
	PlaceTower
	// PlaceSuspended: a Citadel that is suspended (docs/guilda/cidadela-e-torres.md#cidadela-suspensa).
	PlaceSuspended
)

// Placement tells the panel where the machine stands and where the Citadel of now
// is. The engine implements it.
type Placement interface {
	Place() Place
	// CitadelURL is the address of the panel of the Citadel of now, without a
	// trailing slash ("https://mestre.local:7443"); empty when this machine does
	// not know it.
	CitadelURL() string
}

// Gate puts the placement in front of the panel: the Citadel that acts serves it
// through next; a Tower answers 307 to the Citadel of now, keeping the path and
// the query, and gives no data of its own; a suspended Citadel does the same when
// it knows the Citadel of now, and shows only a page "Cidadela suspensa" when it
// does not.
//
// Part of CA-CIDADELA-08 (the 307 of the redirected panel).
func Gate(pl Placement, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		place := pl.Place()
		if place == PlaceCitadel {
			next.ServeHTTP(w, r)
			return
		}
		if url := pl.CitadelURL(); url != "" {
			w.Header().Set("Location", url+r.URL.RequestURI())
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		if place == PlaceSuspended {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(suspendedPage))
			return
		}
		// a Tower that does not know the Citadel: nothing to show and nothing to give
		http.Error(w, "the Citadel is not known to this machine", http.StatusServiceUnavailable)
	})
}

// suspendedPage is all a suspended Citadel shows: no login, no actions.
const suspendedPage = `<!doctype html>
<html lang="pt-BR"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Cidadela suspensa</title></head>
<body><main><h1>Cidadela suspensa</h1><p>Confirmando a época. Tente de novo em instantes.</p></main></body></html>
`
