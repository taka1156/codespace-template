// check_image_updates checks Docker Hub for updated image versions in codespacegen.json.
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

var httpClient = &http.Client{}

func getDockerHubTags(imageName string, maxPages int) ([]string, error) {
    url := fmt.Sprintf(
        "https://hub.docker.com/v2/repositories/library/%s/tags?page_size=100&ordering=last_updated",
        imageName,
    )
    var tags []string
    for i := 0; i < maxPages && url != ""; i++ {
        req, err := http.NewRequest(http.MethodGet, url, nil)
        if err != nil {
            return nil, err
        }
        req.Header.Set("User-Agent", "check-image-updates/1.0")

        resp, err := httpClient.Do(req)
        if err != nil {
            return tags, fmt.Errorf("failed to fetch tags for %s: %w", imageName, err)
        }
        body, err := io.ReadAll(resp.Body)
        resp.Body.Close()
        if err != nil {
            return tags, err
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
        if img == "" || !strings.Contains(img, ":") || seen[img] {
            continue
        }
        seen[img] = true

        idx := strings.LastIndex(img, ":")
        name, currentTag := img[:idx], img[idx+1:]

        fmt.Fprintf(os.Stderr, "Checking %s ...\n", img)
        tags, err := getDockerHubTags(name, 3)
        if err != nil {
            fmt.Fprintf(os.Stderr, "[warn] %v\n", err)
            continue
        }

        latestTag := findLatestTag(currentTag, tags)
        if latestTag != currentTag {
            newImg := name + ":" + latestTag
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
