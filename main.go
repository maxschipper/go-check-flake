package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// --- Structs for flake.lock parsing ---

type FlakeLock struct {
	Root  string          `json:"root"`
	Nodes map[string]Node `json:"nodes"`
}

type Node struct {
	Inputs   map[string]any `json:"inputs"`
	Locked   LockedData     `json:"locked"`
	Original OriginalData   `json:"original"`
}

type LockedData struct {
	Type         string `json:"type"`
	Owner        string `json:"owner"`
	Repo         string `json:"repo"`
	LastModified int64  `json:"lastModified"`
	Rev          string `json:"rev"`
}

type OriginalData struct {
	Ref string `json:"ref"`
}

// --- Structs for GitHub API response ---

type GitHubBranch struct {
	Commit struct {
		Commit struct {
			Committer struct {
				Date string `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	} `json:"commit"`
}

type GitHubRepo struct {
	DefaultBranch string `json:"default_branch"`
}

// --- Configuration ---

// Mutex to prevent print outputs from overlapping
var outputMutex sync.Mutex

func getFlakeLockPath() string {
	path := os.Getenv("NH_FLAKE")
	if path == "" {
		path = "."
	}
	return filepath.Join(path, "flake.lock")
}

// --- Helpers ---

func getTokenFromGhCLI() string {
	cmd := exec.Command("gh", "auth", "token")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func getUpstreamInfo(owner, repo, branchHint, token string) (int64, time.Time, error) {
	client := &http.Client{Timeout: 10 * time.Second}

	makeReq := func(url string) (*http.Response, error) {
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		if token != "" {
			req.Header.Set("Authorization", "token "+token)
		}
		return client.Do(req)
	}

	// 1. Determine Branch
	branch := branchHint
	if branch == "" {
		resp, err := makeReq(fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo))
		if err != nil {
			return 0, time.Time{}, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return 0, time.Time{}, fmt.Errorf("github api status: %d", resp.StatusCode)
		}

		var repoData GitHubRepo
		if err := json.NewDecoder(resp.Body).Decode(&repoData); err != nil {
			return 0, time.Time{}, err
		}
		branch = repoData.DefaultBranch
	}

	// 2. Get Commit Date
	resp, err := makeReq(fmt.Sprintf("https://api.github.com/repos/%s/%s/branches/%s", owner, repo, branch))
	if err != nil {
		return 0, time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, time.Time{}, fmt.Errorf("github api status: %d", resp.StatusCode)
	}

	var branchData GitHubBranch
	if err := json.NewDecoder(resp.Body).Decode(&branchData); err != nil {
		return 0, time.Time{}, err
	}

	t, err := time.Parse(time.RFC3339, branchData.Commit.Commit.Committer.Date)
	if err != nil {
		return 0, time.Time{}, err
	}

	return t.Unix(), t, nil
}

func checkInput(name string, node Node, token string, onlyOutdated bool) {
	if node.Locked.Type != "github" {
		return
	}

	downstreamTs := node.Locked.LastModified
	downstreamDt := time.Unix(downstreamTs, 0)
	owner := node.Locked.Owner
	repo := node.Locked.Repo
	branchHint := node.Original.Ref

	// Network Request (Done in parallel, no lock needed yet)
	upstreamTs, upstreamDt, err := getUpstreamInfo(owner, repo, branchHint, token)

	// LOCK OUTPUT: Only one goroutine can print at a time
	outputMutex.Lock()
	defer outputMutex.Unlock()

	if err != nil {
		if !onlyOutdated {
			fmt.Printf("Checking %s (%s/%s)...\n", name, owner, repo)
			fmt.Printf("    ❌ Could not fetch upstream info: %v\n", err)
			fmt.Println(strings.Repeat("-", 40))
		}
		return
	}

	isOutdated := downstreamTs < upstreamTs

	if onlyOutdated && !isOutdated {
		return
	}

	fmt.Printf("Checking %s (%s/%s)...\n", name, owner, repo)

	if isOutdated {
		diff := upstreamDt.Sub(downstreamDt)
		fmt.Println("    🚨 UPDATE AVAILABLE")
		fmt.Printf("        Local:    %s\n", downstreamDt.Format(time.RFC1123))
		fmt.Printf("        Upstream: %s\n", upstreamDt.Format(time.RFC1123))
		fmt.Printf("        Lag:      %s\n", diff)
	} else if downstreamTs > upstreamTs {
		diff := downstreamDt.Sub(upstreamDt)
		fmt.Printf("    ⚠️  Local ahead by %s\n", diff)
	} else {
		fmt.Println("    ✅ Up to date")
	}

	fmt.Println(strings.Repeat("-", 40))
}

// --- Main ---

func main() {
	all := flag.Bool("a", false, "Check all inputs and show all results")
	allOutdated := flag.Bool("A", false, "Check all inputs but ONLY show outdated ones")

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [flags] [flake_input]\n", os.Args[0])
		flag.PrintDefaults()
	}

	flag.Parse()

	flakeInput := "nixpkgs"
	args := flag.Args()
	if len(args) > 0 {
		flakeInput = args[0]
	}

	checkAll := *all || *allOutdated
	onlyOutdated := *allOutdated

	if checkAll {
		flakeInput = ""
	}

	lockPath := getFlakeLockPath()
	file, err := os.Open(lockPath)
	if err != nil {
		fmt.Printf("Error reading lock file: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	byteValue, _ := io.ReadAll(file)
	var lockData FlakeLock
	if err := json.Unmarshal(byteValue, &lockData); err != nil {
		fmt.Printf("Error parsing json: %v\n", err)
		os.Exit(1)
	}

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = getTokenFromGhCLI()
	}

	if token == "" && checkAll {
		fmt.Println("⚠️  Warning: Rate limits may apply without a token.")
		if !onlyOutdated {
			fmt.Println("ℹ️  Tip: Set GITHUB_TOKEN or use 'gh auth login' to avoid rate limits.")
		}
	}

	if checkAll {
		rootNodeName := lockData.Root
		if rootNodeName == "" {
			rootNodeName = "root"
		}

		var inputsToCheck []string

		rootNode, ok := lockData.Nodes[rootNodeName]
		if ok && len(rootNode.Inputs) > 0 {
			for inputName := range rootNode.Inputs {
				inputsToCheck = append(inputsToCheck, inputName)
			}
		} else {
			for nodeName := range lockData.Nodes {
				inputsToCheck = append(inputsToCheck, nodeName)
			}
		}

		if len(inputsToCheck) == 0 && !onlyOutdated {
			fmt.Println("No inputs found to check.")
		}

		// PARALLELIZATION START
		var wg sync.WaitGroup

		for _, name := range inputsToCheck {
			var nodeKey = name

			if ok {
				if val, exists := rootNode.Inputs[name]; exists {
					if strVal, isString := val.(string); isString {
						nodeKey = strVal
					} else {
						nodeKey = name
					}
				}
			}

			if node, exists := lockData.Nodes[nodeKey]; exists {
				wg.Add(1)
				// Launch a goroutine
				go func(n string, nd Node) {
					defer wg.Done()
					checkInput(n, nd, token, onlyOutdated)
				}(name, node)
			}
		}

		wg.Wait()

	} else {
		node, ok := lockData.Nodes[flakeInput]
		if !ok {
			fmt.Printf("Error: Input '%s' not found.\n", flakeInput)
			os.Exit(1)
		}
		// Single input check doesn't need goroutines, but using the locked version is fine
		checkInput(flakeInput, node, token, false)
	}
}
