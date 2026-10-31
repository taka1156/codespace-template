// check_image_updates checks container registries (Docker Hub and GHCR) for updated image versions in codespacegen.json.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	Schema string          `json:"$schema,omitempty"`
	Common json.RawMessage `json:"common,omitempty"`
	Langs  []Lang          `json:"langs"`
}

type Lang struct {
	ProfileName      string   `json:"profileName"`
	Image            string   `json:"image"`
	VscodeExtensions []string `json:"vscodeExtensions,omitempty"`
	RunCommand       string   `json:"runCommand,omitempty"`
	LinuxPackages    []string `json:"linuxPackages,omitempty"`
}

type hubResponse struct {
	Next    *string  `json:"next"`
	Results []hubTag `json:"results"`
}

type hubTag struct {
	Name string `json:"name"`
}

type registryTagList struct {
	Tags []string `json:"tags"`
}

type registryToken struct {
	Token string `json:"token"`
}

var httpClient = &http.Client{}

// テスト時に差し替えられるよう、ベース URL は変数にしている。
var (
	hubBase  = "https://hub.docker.com"
	ghcrBase = "https://ghcr.io"
)

// imageRef はイメージ指定(例: node:26-slim / ghcr.io/voidzero-dev/vite-plus:1.4.0)を分解したもの。
type imageRef struct {
	registry string // "docker.io" / "ghcr.io" など
	repo     string // レジストリ内のリポジトリ名(例: library/node, voidzero-dev/vite-plus)
	name     string // 元の表記のイメージ名(タグなし)。更新後のイメージ名の組み立てに使う
	tag      string
}

// parseImage はタグ付きのイメージ指定を分解する。
// タグがない指定とダイジェスト指定(@sha256:...)は対象外として false を返す。
func parseImage(img string) (imageRef, bool) {
	if img == "" || strings.Contains(img, "@") {
		return imageRef{}, false
	}
	slash := strings.LastIndex(img, "/")
	colon := strings.LastIndex(img, ":")
	// localhost:5000/foo のように、ポート番号のコロンをタグの区切りと取り違えない
	if colon == -1 || colon < slash {
		return imageRef{}, false
	}
	name, tag := img[:colon], img[colon+1:]

	registry, repo := "docker.io", name
	if i := strings.Index(name, "/"); i != -1 {
		first := name[:i]
		if strings.ContainsAny(first, ".:") || first == "localhost" {
			registry, repo = first, name[i+1:]
		}
	}
	if registry == "docker.io" && !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}
	return imageRef{registry: registry, repo: repo, name: name, tag: tag}, true
}

// httpGet は GET して本文とヘッダーを返す。200 以外はエラーにする
// (以前は 404 などでも空の結果として扱われ、「最新です」と誤判定していた)。
func httpGet(url string, headers map[string]string) ([]byte, http.Header, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "check-image-updates/1.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	return body, resp.Header, nil
}

func getDockerHubTags(repo string, maxPages int) ([]string, error) {
	url := fmt.Sprintf(
		"%s/v2/repositories/%s/tags?page_size=100&ordering=last_updated",
		hubBase, repo,
	)
	var tags []string
	for i := 0; i < maxPages && url != ""; i++ {
		body, _, err := httpGet(url, nil)
		if err != nil {
			return tags, fmt.Errorf("failed to fetch tags for %s: %w", repo, err)
		}
		var data hubResponse
		if err := json.Unmarshal(body, &data); err != nil {
			return tags, err
		}
		for _, t := range data.Results {
			tags = append(tags, t.Name)
		}
		if data.Next == nil || *data.Next == "" {
			break
		}
		url = *data.Next
	}
	return tags, nil
}

var nextLinkRe = regexp.MustCompile(`<([^>]+)>\s*;\s*rel="next"`)

// getRegistryTags は OCI Distribution API で匿名にタグ一覧を取得する(GHCR の公開イメージ向け)。
// タグは更新順ではなく辞書順で返るため、ページを辿って全件取得する。
func getRegistryTags(base, repo string, maxPages int) ([]string, error) {
	body, _, err := httpGet(fmt.Sprintf("%s/token?scope=repository:%s:pull", base, repo), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get token for %s: %w", repo, err)
	}
	var tok registryToken
	if err := json.Unmarshal(body, &tok); err != nil || tok.Token == "" {
		return nil, fmt.Errorf("failed to get token for %s", repo)
	}
	auth := map[string]string{"Authorization": "Bearer " + tok.Token}

	var tags []string
	next := fmt.Sprintf("%s/v2/%s/tags/list?n=1000", base, repo)
	for i := 0; i < maxPages && next != ""; i++ {
		body, hdr, err := httpGet(next, auth)
		if err != nil {
			return tags, fmt.Errorf("failed to fetch tags for %s: %w", repo, err)
		}
		var data registryTagList
		if err := json.Unmarshal(body, &data); err != nil {
			return tags, err
		}
		tags = append(tags, data.Tags...)

		next = ""
		if m := nextLinkRe.FindStringSubmatch(hdr.Get("Link")); m != nil {
			next = m[1]
			if strings.HasPrefix(next, "/") {
				next = base + next
			}
		}
	}
	return tags, nil
}

func getTags(ref imageRef) ([]string, error) {
	switch ref.registry {
	case "docker.io":
		return getDockerHubTags(ref.repo, 3)
	case "ghcr.io":
		return getRegistryTags(ghcrBase, ref.repo, 20)
	default:
		return nil, fmt.Errorf("unsupported registry: %s", ref.registry)
	}
}

var tagRe = regexp.MustCompile(`^(\d+(?:\.\d+)*)((?:-.+)?)$`)

type parsedTag struct {
	version []int
	suffix  string
}

func parseTag(tag string) (parsedTag, bool) {
	m := tagRe.FindStringSubmatch(tag)
	if m == nil {
		return parsedTag{}, false
	}
	parts := strings.Split(m[1], ".")
	version := make([]int, len(parts))
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil {
			return parsedTag{}, false
		}
		version[i] = v
	}
	return parsedTag{version: version, suffix: m[2]}, true
}

func versionGreater(a, b []int) bool {
	for i := range a {
		if i >= len(b) {
			return true
		}
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func findLatestTag(currentTag string, available []string) string {
	cur, ok := parseTag(currentTag)
	if !ok {
		return currentTag
	}
	bestVersion := cur.version
	bestTag := currentTag
	for _, tag := range available {
		p, ok := parseTag(tag)
		if !ok {
			continue
		}
		if p.suffix != cur.suffix || len(p.version) != len(cur.version) {
			continue
		}
		if versionGreater(p.version, bestVersion) {
			bestVersion = p.version
			bestTag = tag
		}
	}
	return bestTag
}

func main() {
	configPath := "codespacegen.json"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("failed to read config: %v", err)
	}
	var config Config
	if err := json.Unmarshal(raw, &config); err != nil {
		log.Fatalf("failed to parse config: %v", err)
	}

	seen := make(map[string]bool)
	updates := make(map[string]string)

	for _, lang := range config.Langs {
		img := lang.Image
		if seen[img] {
			continue
		}
		seen[img] = true

		ref, ok := parseImage(img)
		if !ok {
			continue
		}

		fmt.Fprintf(os.Stderr, "Checking %s ...\n", img)
		tags, err := getTags(ref)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[warn] %v\n", err)
			continue
		}

		latestTag := findLatestTag(ref.tag, tags)
		if latestTag != ref.tag {
			newImg := ref.name + ":" + latestTag
			updates[img] = newImg
			fmt.Fprintf(os.Stderr, "  -> %s\n", newImg)
		} else {
			fmt.Fprintln(os.Stderr, "  up to date")
		}
	}

	if len(updates) == 0 {
		fmt.Println("All images are up to date.")
		return
	}

	for i := range config.Langs {
		if newImg, ok := updates[config.Langs[i].Image]; ok {
			config.Langs[i].Image = newImg
		}
	}

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		log.Fatalf("failed to marshal config: %v", err)
	}
	out = append(out, '\n')

	if err := os.WriteFile(configPath, out, 0o644); err != nil {
		log.Fatalf("failed to write config: %v", err)
	}

	fmt.Println("\nUpdated codespacegen.json:")
	for old, nw := range updates {
		fmt.Printf("  %s  ->  %s\n", old, nw)
	}
}
