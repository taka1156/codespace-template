package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ---- ヘルパー ----

// setVar はグローバル変数を一時的に差し替え、テスト終了時に元へ戻す。
func setVar[T any](t *testing.T, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

// useHub は Docker Hub の向き先をテスト用サーバーに差し替える。
func useHub(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	setVar(t, &hubBase, srv.URL)
	return srv
}

// useNodeDist は Node.js のリリース一覧の向き先を差し替え、キャッシュも消す。
func useNodeDist(t *testing.T, h http.Handler) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	setVar(t, &nodeDistURL, srv.URL)
	setVar(t, &nodeLTSCache, nil)
}

func profileNames(langs []Lang) []string {
	names := make([]string, len(langs))
	for i, l := range langs {
		names[i] = l.ProfileName
	}
	return names
}

const nodeReleasesJSON = `[
	{"version":"v27.1.0","lts":false},
	{"version":"v26.2.0","lts":"Xenon"},
	{"version":"v26.0.0","lts":false},
	{"version":"v25.9.0","lts":false},
	{"version":"v24.11.0","lts":"Krypton"}
]`

// ---- イメージ名・タグの解析 ----

func TestParseImage(t *testing.T) {
	cases := []struct {
		in   string
		want imageRef
		ok   bool
	}{
		{"node:26-slim", imageRef{"docker.io", "library/node", "node", "26-slim"}, true},
		{"ubuntu:26.04", imageRef{"docker.io", "library/ubuntu", "ubuntu", "26.04"}, true},
		{"python:3.15-slim", imageRef{"docker.io", "library/python", "python", "3.15-slim"}, true},
		{"someuser/app:1.2", imageRef{"docker.io", "someuser/app", "someuser/app", "1.2"}, true},
		{"ghcr.io/voidzero-dev/vite-plus:1.4.0", imageRef{"ghcr.io", "voidzero-dev/vite-plus", "ghcr.io/voidzero-dev/vite-plus", "1.4.0"}, true},
		{"localhost:5000/foo:1", imageRef{"localhost:5000", "foo", "localhost:5000/foo", "1"}, true},
		{"localhost/foo:1", imageRef{"localhost", "foo", "localhost/foo", "1"}, true},
		{"localhost:5000/foo", imageRef{}, false},
		{"node", imageRef{}, false},
		{"node@sha256:abc", imageRef{}, false},
		{"", imageRef{}, false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, ok := parseImage(c.in)
			if ok != c.ok || (ok && got != c.want) {
				t.Errorf("parseImage(%q) = %+v, %v; want %+v, %v", c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestParseTag(t *testing.T) {
	cases := []struct {
		in   string
		want parsedTag
		ok   bool
	}{
		{"26", parsedTag{[]int{26}, ""}, true},
		{"26.04", parsedTag{[]int{26, 4}, ""}, true},
		{"1.100-slim", parsedTag{[]int{1, 100}, "-slim"}, true},
		{"3.15-slim-bookworm", parsedTag{[]int{3, 15}, "-slim-bookworm"}, true},
		{"latest", parsedTag{}, false},
		{"v1.2", parsedTag{}, false},
		{"1.", parsedTag{}, false},
		{"sha-abcdef", parsedTag{}, false},
		{"99999999999999999999", parsedTag{}, false}, // int に収まらない
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, ok := parseTag(c.in)
			if ok != c.ok || (ok && !reflect.DeepEqual(got, c.want)) {
				t.Errorf("parseTag(%q) = %+v, %v; want %+v, %v", c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestVersionGreater(t *testing.T) {
	cases := []struct {
		a, b []int
		want bool
	}{
		{[]int{1, 10}, []int{1, 9}, true}, // 数値として比較する（文字列比較なら逆になる）
		{[]int{1, 9}, []int{1, 10}, false},
		{[]int{2}, []int{1}, true},
		{[]int{1, 4}, []int{1, 4}, false}, // 同じなら false
		{[]int{1, 4, 1}, []int{1, 4}, true},
		{[]int{1, 4}, []int{1, 4, 1}, false},
	}
	for _, c := range cases {
		if got := versionGreater(c.a, c.b); got != c.want {
			t.Errorf("versionGreater(%v, %v) = %v; want %v", c.a, c.b, got, c.want)
		}
	}
}

// ---- 最新タグの選択 ----

func TestFindLatestTag(t *testing.T) {
	cases := []struct {
		name    string
		current string
		tags    []string
		want    string
	}{
		{
			name:    "GHCR の 3 桁バージョン",
			current: "1.4.0",
			tags:    []string{"1", "1.4", "1.4.0", "1.4.1", "1.5.0", "1.10.0", "latest", "pr-2313", "sha-abcdef", "2"},
			want:    "1.10.0",
		},
		{
			name:    "桁数が違うタグには乗り換えない",
			current: "1.4",
			tags:    []string{"1", "1.4", "1.4.0", "1.5.0", "2"},
			want:    "1.4",
		},
		{
			name:    "サフィックスが同じものだけを見る",
			current: "1.100-slim",
			tags:    []string{"1.101", "1.101-alpine", "1.101-slim", "1.102-slim-bookworm"},
			want:    "1.101-slim",
		},
		{
			name:    "古いタグには下げない",
			current: "3.15-slim",
			tags:    []string{"3.13-slim", "3.14-slim"},
			want:    "3.15-slim",
		},
		{
			name:    "解析できない現在タグはそのまま",
			current: "latest",
			tags:    []string{"1.0", "2.0"},
			want:    "latest",
		},
		{
			name:    "タグ一覧が空",
			current: "1.27",
			tags:    nil,
			want:    "1.27",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := findLatestTag(c.current, c.tags, nil); got != c.want {
				t.Errorf("findLatestTag(%q) = %q; want %q", c.current, got, c.want)
			}
		})
	}
}

// ---- レジストリからのタグ取得 ----

func TestGetRegistryTagsPaginationAndAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	t.Cleanup(srv.Close)

	got, err := getRegistryTags(srv.URL, "voidzero-dev/vite-plus", 5)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"1", "1.4", "1.4.0", "1.5.0", "latest"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGetRegistryTagsRespectsMaxPages(t *testing.T) {
	var listCalls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			fmt.Fprint(w, `{"token":"tkn"}`)
			return
		}
		listCalls++
		// 常に次のページがあると返し続ける
		w.Header().Set("Link", fmt.Sprintf(`</v2/x/y/tags/list?last=%d>; rel="next"`, listCalls))
		fmt.Fprintf(w, `{"tags":["%d.0"]}`, listCalls)
	}))
	t.Cleanup(srv.Close)

	got, err := getRegistryTags(srv.URL, "x/y", 3)
	if err != nil {
		t.Fatal(err)
	}
	if listCalls != 3 || len(got) != 3 {
		t.Errorf("calls = %d, tags = %v; want 3 calls and 3 tags", listCalls, got)
	}
}

func TestGetRegistryTagsTokenErrors(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"HTTP エラー": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		},
		"トークンが空": func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"token":""}`)
		},
		"JSON が壊れている": func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{not json`)
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			t.Cleanup(srv.Close)
			if _, err := getRegistryTags(srv.URL, "x/y", 1); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestGetDockerHubTags(t *testing.T) {
	var srv *httptest.Server
	srv = useHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestGetDockerHubTagsRespectsMaxPages(t *testing.T) {
	var srv *httptest.Server
	calls := 0
	srv = useHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprintf(w, `{"next":"%s/v2/repositories/library/node/tags?page=%d","results":[{"name":"%d"}]}`, srv.URL, calls+1, calls)
	}))

	got, err := getDockerHubTags("library/node", 2)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || len(got) != 2 {
		t.Errorf("calls = %d, tags = %v; want 2 calls and 2 tags", calls, got)
	}
}

func TestHTTPErrorIsNotSilentlyUpToDate(t *testing.T) {
	useHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"httperror 404: object not found"}`)
	}))

	if _, err := getDockerHubTags("library/ghcr.io/voidzero-dev/vite-plus", 3); err == nil {
		t.Error("404 は空の結果ではなくエラーとして扱われるべき")
	}
}

func TestGetTagsUnsupportedRegistry(t *testing.T) {
	if _, err := getTags(imageRef{registry: "quay.io", repo: "foo/bar"}); err == nil {
		t.Error("未対応のレジストリはエラーになるべき")
	}
}

// ---- LTS フィルター ----

func TestNodeLTSFilter(t *testing.T) {
	useNodeDist(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, nodeReleasesJSON)
	}))

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

func TestNodeLTSCacheAvoidsRefetch(t *testing.T) {
	calls := 0
	useNodeDist(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, nodeReleasesJSON)
	}))

	for i := 0; i < 3; i++ {
		if _, err := nodeLTSMajors(); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Errorf("release list fetched %d times; want 1", calls)
	}
}

func TestNodeLTSFetchErrors(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"HTTP 500": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
		"JSON が壊れている": func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `[{`)
		},
		"LTS が 1 つもない": func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `[{"version":"v27.0.0","lts":false}]`)
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			useNodeDist(t, h)
			if _, err := ltsFilter(imageRef{registry: "docker.io", repo: "library/node"}); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestUbuntuLTSFilter(t *testing.T) {
	filter, err := ltsFilter(imageRef{registry: "docker.io", repo: "library/ubuntu"})
	if err != nil {
		t.Fatal(err)
	}
	tags := []string{"24.04", "24.10", "25.04", "25.10", "26.04", "26.10", "27.04"}
	cases := []struct{ current, want string }{
		{"24.04", "26.04"},
		{"26.04", "26.04"},
		{"26.10", "26.10"}, // LTS でない現在タグから、それより古い LTS には下げない
	}
	for _, c := range cases {
		if got := findLatestTag(c.current, tags, filter); got != c.want {
			t.Errorf("findLatestTag(%q) = %q; want %q", c.current, got, c.want)
		}
	}
}

func TestNoLTSFilterForOtherImages(t *testing.T) {
	for _, ref := range []imageRef{
		{registry: "docker.io", repo: "library/python"},
		{registry: "docker.io", repo: "library/golang"},
		{registry: "ghcr.io", repo: "voidzero-dev/vite-plus"},
		{registry: "ghcr.io", repo: "library/node"}, // docker.io 以外の node には適用しない
	} {
		filter, err := ltsFilter(ref)
		if err != nil || filter != nil {
			t.Errorf("ltsFilter(%+v) = %v, %v; want nil, nil", ref, filter != nil, err)
		}
	}
}

// ---- langs の並べ替え ----

func TestLangGroupRank(t *testing.T) {
	rank := func(name string) int {
		for i, g := range groupOrder {
			if g == name {
				return i
			}
		}
		t.Fatalf("%q is not in groupOrder", name)
		return -1
	}
	cases := []struct {
		lang Lang
		want int
	}{
		{Lang{ProfileName: "Claude Sandbox", Image: "ubuntu:26.04"}, rank("claude sandbox")},
		{Lang{ProfileName: "GO", Image: "golang:1.27"}, rank("go")}, // 大文字小文字は区別しない
		{Lang{ProfileName: "node:astro", Image: "node:26-slim"}, rank("node")},
		{Lang{ProfileName: "vite-plus:node-ommit", Image: "node:26-slim"}, rank("vite-plus")}, // profileName がイメージより優先
		{Lang{ProfileName: "python", Image: "python:3.15-slim"}, rank("python")},
		{Lang{ProfileName: "django", Image: "python:3.15-slim"}, rank("python")}, // イメージから推測
		{Lang{ProfileName: "nextjs", Image: "node:26-slim"}, rank("node")},
		{Lang{ProfileName: "zig", Image: "ubuntu:26.10"}, len(groupOrder)},           // 不明は末尾
		{Lang{ProfileName: "mystery", Image: "ghcr.io/foo/node:1"}, len(groupOrder)}, // docker.io 以外は推測しない
	}
	for _, c := range cases {
		t.Run(c.lang.ProfileName, func(t *testing.T) {
			if got := langGroupRank(c.lang); got != c.want {
				t.Errorf("langGroupRank(%+v) = %d; want %d", c.lang, got, c.want)
			}
		})
	}
}

func TestSortLangs(t *testing.T) {
	langs := []Lang{
		{ProfileName: "zig", Image: "ubuntu:26.10"},
		{ProfileName: "Claude Sandbox", Image: "ubuntu:26.04"},
		{ProfileName: "go", Image: "golang:1.27"},
		{ProfileName: "rust", Image: "rust:1.100-slim"},
		{ProfileName: "moonbit", Image: "ubuntu:26.10"},
		{ProfileName: "vite-plus", Image: "ubuntu:26.04"},
		{ProfileName: "vite-plus:node-ommit", Image: "node:26-slim"},
		{ProfileName: "node:biome", Image: "node:26-slim"},
		{ProfileName: "node:react", Image: "node:26-slim"},
		{ProfileName: "node:astro", Image: "node:26-slim"},
		{ProfileName: "python", Image: "python:3.15-slim"},
		{ProfileName: "gcc", Image: "ubuntu:26.10"},
	}
	if !sortLangs(langs) {
		t.Error("sortLangs returned false; want true")
	}
	want := []string{
		"Claude Sandbox",
		"gcc", "rust", "go",
		"moonbit", "python",
		"node:biome", "node:react", "node:astro", // 同じグループ内は元の順番のまま
		"vite-plus", "vite-plus:node-ommit",
		"zig", // 不明なものは末尾
	}
	if got := profileNames(langs); !reflect.DeepEqual(got, want) {
		t.Errorf("order =\n  %v\nwant\n  %v", got, want)
	}

	// 並べ替え済みならもう一度呼んでも変わらない
	if sortLangs(langs) {
		t.Error("second sortLangs returned true; want false")
	}
	if got := profileNames(langs); !reflect.DeepEqual(got, want) {
		t.Errorf("order changed on second sort: %v", got)
	}
}

func TestSortLangsEmpty(t *testing.T) {
	if sortLangs(nil) {
		t.Error("sortLangs(nil) returned true")
	}
}

// ---- main を通した結合テスト ----

func runMain(t *testing.T, configPath string) {
	t.Helper()
	setVar(t, &os.Args, []string{"check-image-updates", configPath})
	// 進捗ログ (stderr) と結果 (stdout) はテスト出力を汚さないよう捨てる
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { devNull.Close() })
	setVar(t, &os.Stdout, devNull)
	setVar(t, &os.Stderr, devNull)
	main()
}

func TestMainUpdatesImagesAndSortsLangs(t *testing.T) {
	hubTags := map[string][]string{
		"library/golang": {"1.27", "1.28", "1.28-alpine"},
		"library/node":   {"26-slim", "27-slim", "28-slim"},
		"library/ubuntu": {"26.04", "26.10", "27.04"},
		"library/python": {"3.15-slim", "3.16-slim"},
	}
	useHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		repo := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v2/repositories/"), "/tags")
		tags, ok := hubTags[repo]
		if !ok {
			http.NotFound(w, r)
			return
		}
		results := make([]map[string]string, len(tags))
		for i, tag := range tags {
			results[i] = map[string]string{"name": tag}
		}
		json.NewEncoder(w).Encode(map[string]any{"next": nil, "results": results})
	}))
	useNodeDist(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, nodeReleasesJSON)
	}))

	path := filepath.Join(t.TempDir(), "codespacegen.json")
	input := `{
  "$schema": "https://example.com/schema.json",
  "common": {"timezone": "Asia/Tokyo"},
  "langs": [
    {"profileName": "node:react", "image": "node:26-slim"},
    {"profileName": "go", "image": "golang:1.27"},
    {"profileName": "python", "image": "python:3.15-slim"},
    {"profileName": "Claude Sandbox", "image": "ubuntu:26.04", "runCommand": "a && b"},
    {"profileName": "unknown-registry", "image": "quay.io/foo/bar:1.0"}
  ]
}`
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	runMain(t, path)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Config
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, raw)
	}

	wantImages := []struct{ profile, image string }{
		{"Claude Sandbox", "ubuntu:26.04"}, // 27.04 は LTS でないので据え置き
		{"go", "golang:1.28"},              // -alpine には乗り換えない
		{"python", "python:3.16-slim"},
		{"node:react", "node:26-slim"},              // 27/28 は LTS でないので据え置き
		{"unknown-registry", "quay.io/foo/bar:1.0"}, // 未対応レジストリは触らない
	}
	if len(got.Langs) != len(wantImages) {
		t.Fatalf("langs = %v", profileNames(got.Langs))
	}
	for i, w := range wantImages {
		if got.Langs[i].ProfileName != w.profile || got.Langs[i].Image != w.image {
			t.Errorf("langs[%d] = %s (%s); want %s (%s)",
				i, got.Langs[i].ProfileName, got.Langs[i].Image, w.profile, w.image)
		}
	}
	if got.Schema != "https://example.com/schema.json" {
		t.Errorf("$schema = %q; was lost", got.Schema)
	}
	if !strings.Contains(string(got.Common), "Asia/Tokyo") {
		t.Errorf("common = %s; was lost", got.Common)
	}
	if got.Langs[0].RunCommand != "a && b" {
		t.Errorf("runCommand = %q; was changed", got.Langs[0].RunCommand)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Error("output should end with a newline")
	}
}

func TestMainLeavesFileUntouchedWhenNothingChanges(t *testing.T) {
	useHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"next":null,"results":[{"name":"1.27"}]}`)
	}))

	path := filepath.Join(t.TempDir(), "codespacegen.json")
	// 並び順も正しく、更新もない。わざと整形を崩しておき、書き換えられていないことを確かめる
	input := `{"langs":[{"profileName":"gcc","image":"ubuntu:26.10"},   {"profileName":"go","image":"golang:1.27"}]}`
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}
	// ubuntu の LTS 判定は外部に問い合わせないが、タグ一覧に 26.10 より新しい LTS が無いので据え置きになる

	runMain(t, path)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != input {
		t.Errorf("file was rewritten:\n%s", raw)
	}
}
