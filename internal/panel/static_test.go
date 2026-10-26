package panel_test

import "testing"

// The tests never depend on the build of the front end: the files are a MapFS.
func TestStaticFilesAndTheFallbackToIndex(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	for _, c := range []struct {
		path, body, cache string
		code              int
	}{
		{"/", "<html>app</html>", "no-cache", 200},
		{"/mesas/3/canvas", "<html>app</html>", "no-cache", 200}, // a route of the client
		{"/assets/app-1a2b3c.js", "console.log(1)", "public, max-age=31536000, immutable", 200},
		{"/assets/falta.js", "", "", 404},
	} {
		w := r.call("GET", c.path, nil, opts{})
		if w.Code != c.code || (c.code == 200 && w.Body.String() != c.body) || (c.cache != "" && w.Header().Get("Cache-Control") != c.cache) {
			t.Errorf("%s = %d %q cache %q", c.path, w.Code, w.Body, w.Header().Get("Cache-Control"))
		}
		if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s has no security headers", c.path)
		}
	}
	if w := r.call("POST", "/mesas", nil, opts{}); w.Code != 405 {
		t.Errorf("POST on a client route = %d", w.Code)
	}
}

func TestWithoutABuildTheAnswerSaysSo(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	delete(r.assets, "index.html")
	if w := r.call("GET", "/", nil, opts{}); w.Code != 503 {
		t.Fatalf("no index = %d", w.Code)
	}
}
