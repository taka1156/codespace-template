package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
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

type nodeRelease struct {
	Version string `json:"version"`
	LTS     any    `json:"lts"`
}

var httpClient = &http.Client{}

var (
	hubBase     = "https://hub.docker.com"
	ghcrBase    = "https://ghcr.io"
	nodeDistURL = "https://nodejs.org/dist/index.json"
)

type imageRef struct {
	registry string
	repo     string
	name     string
	tag      string
}

func parseImage(img string) (imageRef, bool) {
	if img == "" || strings.Contains(img, "@") {
		return imageRef{}, false
	}
	slash := strings.LastIndex(img, "/")
	colon := strings.LastIndex(img, ":")
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

type tagFilter func(parsedTag) bool

var nodeLTSCache map[int]bool

func nodeLTSMajors() (map[int]bool, error) {
	if nodeLTSCache != nil {
		return nodeLTSCache, nil
	}
	body, _, err := httpGet(nodeDistURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Node.js releases: %w", err)
	}
	var rels []nodeRelease
	if err := json.Unmarshal(body, &rels); err != nil {
		return nil, fmt.Errorf("failed to parse Node.js releases: %w", err)
	}
	majors := make(map[int]bool)
	for _, r := range rels {
		if name, ok := r.LTS.(string); !ok || name == "" {
			continue
		}
		v := strings.TrimPrefix(r.Version, "v")
		major, err := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
		if err != nil {
			continue
		}
		majors[major] = true
	}
	if len(majors) == 0 {
		return nil, fmt.Errorf("no LTS releases found in Node.js release list")
	}
	nodeLTSCache = majors
	return majors, nil
}

func ltsFilter(ref imageRef) (tagFilter, error) {
	if ref.registry != "docker.io" {
		return nil, nil
	}
	switch ref.repo {
	case "library/node":
		majors, err := nodeLTSMajors()
		if err != nil {
			return nil, err
		}
		return func(p parsedTag) bool { return majors[p.version[0]] }, nil
	case "library/ubuntu":
		return func(p parsedTag) bool {
			return len(p.version) >= 2 && p.version[0]%2 == 0 && p.version[1] == 4
		}, nil
	}
	return nil, nil
}

func findLatestTag(currentTag string, available []string, filter tagFilter) string {
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
		if filter != nil && !filter(p) {
			continue
		}
		if versionGreater(p.version, bestVersion) {
			bestVersion = p.version
			bestTag = tag
		}
	}
	return bestTag
}

// groupOrder は langs の並び順を決めるグループ（profileName の ":" より前）の順番。
// ツール系 → ネイティブ言語 → その他の言語 → JavaScript/Node 系 の順に並べる。
var groupOrder = []string{
	"claude sandbox",
	"gcc",
	"rust",
	"go",
	"moonbit",
	"python",
	"node",
	"vite-plus",
}

// imageGroups は profileName が groupOrder に無いときに、イメージ名からグループを推測するための表。
var imageGroups = map[string]string{
	"library/gcc":    "gcc",
	"library/rust":   "rust",
	"library/golang": "go",
	"library/node":   "node",
	"library/python": "python",
}

// langGroupRank はプロファイルの並び順の重みを返す。どのグループにも当てはまらないものは末尾に回す。
func langGroupRank(lang Lang) int {
	base := strings.ToLower(strings.TrimSpace(strings.SplitN(lang.ProfileName, ":", 2)[0]))
	for i, g := range groupOrder {
		if base == g {
			return i
		}
	}
	if ref, ok := parseImage(lang.Image); ok && ref.registry == "docker.io" {
		if g, ok := imageGroups[ref.repo]; ok {
			for i, name := range groupOrder {
				if name == g {
					return i
				}
			}
		}
	}
	return len(groupOrder)
}

// sortLangs は langs をグループ順に並べ替える。同じグループ内では元の順番を保つ。
// 並び順が変わったら true を返す。
func sortLangs(langs []Lang) bool {
	before := make([]string, len(langs))
	for i, l := range langs {
		before[i] = l.ProfileName
	}
	sort.SliceStable(langs, func(i, j int) bool {
		return langGroupRank(langs[i]) < langGroupRank(langs[j])
	})
	for i, l := range langs {
		if before[i] != l.ProfileName {
			return true
		}
	}
	return false
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

		filter, err := ltsFilter(ref)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[warn] %v\n", err)
			continue
		}
		if filter != nil {
			fmt.Fprintln(os.Stderr, "  (LTS only)")
		}

		latestTag := findLatestTag(ref.tag, tags, filter)
		if latestTag != ref.tag {
			newImg := ref.name + ":" + latestTag
			updates[img] = newImg
			fmt.Fprintf(os.Stderr, "  -> %s\n", newImg)
		} else {
			fmt.Fprintln(os.Stderr, "  up to date")
		}
	}

	for i := range config.Langs {
		if newImg, ok := updates[config.Langs[i].Image]; ok {
			config.Langs[i].Image = newImg
		}
	}

	reordered := sortLangs(config.Langs)

	if len(updates) == 0 && !reordered {
		fmt.Println("All images are up to date.")
		return
	}

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		log.Fatalf("failed to marshal config: %v", err)
	}
	out = append(out, '\n')

	if err := os.WriteFile(configPath, out, 0o644); err != nil {
		log.Fatalf("failed to write config: %v", err)
	}

	fmt.Printf("\nUpdated %s:\n", configPath)
	if len(updates) == 0 {
		fmt.Println("  All images are up to date.")
	}
	for old, nw := range updates {
		fmt.Printf("  %s  ->  %s\n", old, nw)
	}
	if reordered {
		fmt.Println("  langs reordered by group:")
		for _, l := range config.Langs {
			fmt.Printf("    - %s\n", l.ProfileName)
		}
	}
}