package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestParseImage(t *testing.T) {
	cases := []struct {
		in   string
		want imageRef
		ok   bool
	}{
		{"node:26-slim", imageRef{"docker.io", "library/node", "node", "26-slim"}, true},
		{"ubuntu:26.04", imageRef{"docker.io", "library/ubuntu", "ubuntu", "26.04"}, true},
		{"someuser/app:1.2", imageRef{"docker.io", "someuser/app", "someuser/app", "1.2"}, true},
		{"ghcr.io/voidzero-dev/vite-plus:1.4.0", imageRef{"ghcr.io", "voidzero-dev/vite-plus", "ghcr.io/voidzero-dev/vite-plus", "1.4.0"}, true},
		{"localhost:5000/foo:1", imageRef{"localhost:5000", "foo", "localhost:5000/foo", "1"}, true},
		{"localhost:5000/foo", imageRef{}, false},
		{"node", imageRef{}, false},
		{"node@sha256:abc", imageRef{}, false},
		{"", imageRef{}, false},
	}
	for _, c := range cases {
		got, ok := parseImage(c.in)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseImage(%q) = %+v, %v; want %+v, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestFindLatestTagGHCR(t *testing.T) {
	tags := []string{"1", "1.4", "1.4.0", "1.4.1", "1.5.0", "1.10.0", "latest", "pr-2313", "sha-abcdef", "2"}
	if got := findLatestTag("1.4.0", tags, nil); got != "1.10.0" {
		t.Errorf("got %s, want 1.10.0", got)
	}
	if got := findLatestTag("1.4", tags, nil); got != "1.4" {
		t.Errorf("got %s, want 1.4", got)
	}
}

func TestGetRegistryTagsPaginationAndAuth(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if r.URL.Query().Get("scope") != "repository:voidzero-dev/vite-plus:pull" {
				http.Error(w, "bad scope", http.StatusBadRequest)
				return
			}
			fmt.Fprint(w, `{"token":"tkn"}`)
		case "/v2/voidzero-dev/vite-plus/tags/list":
			if r.Header.Get("Authorization") != "Bearer tkn" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			if r.URL.Query().Get("last") == "" {
				w.Header().Set("Link", `</v2/voidzero-dev/vite-plus/tags/list?last=1.4.0&n=1000>; rel="next"`)
				fmt.Fprint(w, `{"name":"voidzero-dev/vite-plus","tags":["1","1.4","1.4.0"]}`)
				return
			}
			fmt.Fprint(w, `{"name":"voidzero-dev/vite-plus","tags":["1.5.0","latest"]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	got, err := getRegistryTags(srv.URL, "voidzero-dev/vite-plus", 5)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1", "1.4", "1.4.0", "1.5.0", "latest"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestHTTPErrorIsNotSilentlyUpToDate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"httperror 404: object not found"}`)
	}))
	defer srv.Close()

	old := hubBase
	hubBase = srv.URL
	defer func() { hubBase = old }()

	if _, err := getDockerHubTags("library/ghcr.io/voidzero-dev/vite-plus", 3); err == nil {
		t.Error("404 は空の結果ではなくエラーとして扱われるべき")
	}
}

func TestGetDockerHubTags(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/repositories/library/node/tags" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("page") == "" {
			fmt.Fprintf(w, `{"next":"%s/v2/repositories/library/node/tags?page=2","results":[{"name":"26-slim"},{"name":"26"}]}`, srv.URL)
			return
		}
		fmt.Fprint(w, `{"next":null,"results":[{"name":"27-slim"}]}`)
	}))
	defer srv.Close()

	old := hubBase
	hubBase = srv.URL
	defer func() { hubBase = old }()

	got, err := getDockerHubTags("library/node", 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"26-slim", "26", "27-slim"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if latest := findLatestTag("26-slim", got, nil); latest != "27-slim" {
		t.Errorf("latest = %s, want 27-slim", latest)
	}
}

func TestNodeLTSFilter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[
			{"version":"v27.1.0","lts":false},
			{"version":"v26.2.0","lts":"Xenon"},
			{"version":"v26.0.0","lts":false},
			{"version":"v25.9.0","lts":false},
			{"version":"v24.11.0","lts":"Krypton"}
		]`)
	}))
	defer srv.Close()

	oldURL, oldCache := nodeDistURL, nodeLTSCache
	nodeDistURL, nodeLTSCache = srv.URL, nil
	defer func() { nodeDistURL, nodeLTSCache = oldURL, oldCache }()

	filter, err := ltsFilter(imageRef{registry: "docker.io", repo: "library/node"})
	if err != nil {
		t.Fatal(err)
	}
	tags := []string{"24-slim", "25-slim", "26-slim", "27-slim", "28-slim"}
	if got := findLatestTag("24-slim", tags, filter); got != "26-slim" {
		t.Errorf("got %s, want 26-slim", got)
	}
	if got := findLatestTag("26-slim", tags, filter); got != "26-slim" {
		t.Errorf("got %s, want 26-slim", got)
	}
}

func TestNodeLTSFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	oldURL, oldCache := nodeDistURL, nodeLTSCache
	nodeDistURL, nodeLTSCache = srv.URL, nil
	defer func() { nodeDistURL, nodeLTSCache = oldURL, oldCache }()

	if _, err := ltsFilter(imageRef{registry: "docker.io", repo: "library/node"}); err == nil {
		t.Error("expected error when Node.js release list is unavailable")
	}
}

func TestUbuntuLTSFilter(t *testing.T) {
	filter, err := ltsFilter(imageRef{registry: "docker.io", repo: "library/ubuntu"})
	if err != nil {
		t.Fatal(err)
	}
	tags := []string{"24.04", "24.10", "25.04", "25.10", "26.04", "26.10", "27.04"}
	if got := findLatestTag("24.04", tags, filter); got != "26.04" {
		t.Errorf("got %s, want 26.04", got)
	}
}

func TestNoLTSFilterForOtherImages(t *testing.T) {
	for _, ref := range []imageRef{
		{registry: "docker.io", repo: "library/python"},
		{registry: "ghcr.io", repo: "voidzero-dev/vite-plus"},
	} {
		filter, err := ltsFilter(ref)
		if err != nil || filter != nil {
			t.Errorf("ltsFilter(%+v) = %v, %v; want nil, nil", ref, filter != nil, err)
		}
	}
}
