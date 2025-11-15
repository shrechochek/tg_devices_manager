// // agent.go
// package main

// import (
// 	"bytes"
// 	"encoding/json"
// 	"fmt"
// 	"io"
// 	"log"
// 	"net/http"
// 	"os"
// 	"os/exec"
// 	"time"
// )

// var (
// 	managerURL = os.Getenv("MANAGER_URL")   // e.g. http://192.168.1.10:8080
// 	deviceID   = os.Getenv("DEVICE_ID")     // unique id, e.g. macbook-01
// 	name       = os.Getenv("DEVICE_NAME")   // human name
// 	secret     = os.Getenv("SHARED_SECRET") // must match manager if used
// 	pollDelay  = 3 * time.Second
// )

// func main() {
// 	if managerURL == "" || deviceID == "" {
// 		log.Fatal("Set MANAGER_URL and DEVICE_ID environment variables")
// 	}
// 	if name == "" {
// 		name = deviceID
// 	}
// 	// register
// 	register()

// 	for {
// 		cmd := poll()
// 		if cmd.ID != "" {
// 			out := execCmd(cmd.Cmd)
// 			postResult(cmd.ID, out)
// 		}
// 		time.Sleep(pollDelay)
// 	}
// }

// type CmdResp struct {
// 	ID  string `json:"id"`
// 	Cmd string `json:"cmd"`
// }

// func register() {
// 	body := map[string]string{"device_id": deviceID, "name": name, "secret": secret}
// 	b, _ := json.Marshal(body)
// 	resp, err := http.Post(managerURL+"/register", "application/json", bytes.NewReader(b))
// 	if err != nil {
// 		log.Printf("register error: %v\n", err)
// 		time.Sleep(2 * time.Second)
// 		register()
// 		return
// 	}
// 	resp.Body.Close()
// 	log.Printf("Registered as %s -> %s\n", deviceID, managerURL)
// }

// func poll() CmdResp {
// 	u := fmt.Sprintf("%s/poll?device_id=%s&secret=%s", managerURL, deviceID, secret)
// 	resp, err := http.Get(u)
// 	if err != nil {
// 		log.Printf("poll error: %v\n", err)
// 		time.Sleep(2 * time.Second)
// 		return CmdResp{}
// 	}
// 	defer resp.Body.Close()
// 	if resp.StatusCode != 200 {
// 		// maybe unknown device / bad secret
// 		body, _ := io.ReadAll(resp.Body)
// 		log.Printf("poll status %d: %s\n", resp.StatusCode, string(body))
// 		return CmdResp{}
// 	}
// 	var r CmdResp
// 	_ = json.NewDecoder(resp.Body).Decode(&r)
// 	return r
// }

// func execCmd(cmd string) string {
// 	log.Printf("Executing: %s\n", cmd)
// 	c := exec.Command("/bin/bash", "-lc", cmd)
// 	var out bytes.Buffer
// 	c.Stdout = &out
// 	c.Stderr = &out
// 	_ = c.Run()
// 	return out.String()
// }

// func postResult(cmdID, output string) {
// 	body := map[string]string{"device_id": deviceID, "secret": secret, "cmd_id": cmdID, "output": output}
// 	b, _ := json.Marshal(body)
// 	resp, err := http.Post(managerURL+"/result", "application/json", bytes.NewReader(b))
// 	if err != nil {
// 		log.Printf("postResult error: %v\n", err)
// 		return
// 	}
// 	defer resp.Body.Close()
// 	log.Printf("Posted result for %s (cmd=%s)\n", deviceID, cmdID)
// }

// agent.go (with cwd handling)
// agent.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

var (
	managerURL = os.Getenv("MANAGER_URL") // e.g. http://192.168.1.10:8080
	deviceID   = os.Getenv("DEVICE_ID")   // unique id
	name       = os.Getenv("DEVICE_NAME")
	secret     = os.Getenv("SHARED_SECRET")
	pollDelay  = 3 * time.Second
	stateFile  = "agent_state.json"
	currentDir = ""
)

type CmdResp struct {
	ID  string `json:"id"`
	Cmd string `json:"cmd"`
}

func main() {
	if managerURL == "" || deviceID == "" {
		log.Fatal("Set MANAGER_URL and DEVICE_ID")
	}
	if name == "" {
		name = deviceID
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = "."
	}
	// load state (cwd) if exists
	loadState()
	if currentDir == "" {
		currentDir = home
	}

	register()

	for {
		cmd := poll()
		if cmd.ID != "" {
			handleCommand(cmd)
		}
		time.Sleep(pollDelay)
	}
}

func loadState() {
	f, err := os.Open(stateFile)
	if err != nil {
		return
	}
	defer f.Close()
	var st struct {
		Cwd string `json:"cwd"`
	}
	if err := json.NewDecoder(f).Decode(&st); err == nil {
		currentDir = st.Cwd
	}
}

func saveState() {
	f, err := os.Create(stateFile + ".tmp")
	if err != nil {
		log.Printf("saveState err: %v\n", err)
		return
	}
	_ = json.NewEncoder(f).Encode(map[string]string{"cwd": currentDir})
	f.Close()
	_ = os.Rename(stateFile+".tmp", stateFile)
}

func register() {
	body := map[string]string{"device_id": deviceID, "name": name, "secret": secret, "cwd": currentDir}
	b, _ := json.Marshal(body)
	resp, err := http.Post(managerURL+"/register", "application/json", bytes.NewReader(b))
	if err != nil {
		log.Printf("register error: %v\n", err)
		time.Sleep(2 * time.Second)
		register()
		return
	}
	resp.Body.Close()
	log.Printf("Registered %s -> %s (cwd=%s)\n", deviceID, managerURL, currentDir)
}

func poll() CmdResp {
	u := fmt.Sprintf("%s/poll?device_id=%s&secret=%s", managerURL, deviceID, secret)
	resp, err := http.Get(u)
	if err != nil {
		log.Printf("poll error: %v\n", err)
		time.Sleep(2 * time.Second)
		return CmdResp{}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("poll status %d: %s\n", resp.StatusCode, string(body))
		return CmdResp{}
	}
	var r CmdResp
	_ = json.NewDecoder(resp.Body).Decode(&r)
	return r
}

func handleCommand(c CmdResp) {
	// If the command is "cd" or starts with "cd ", handle it specially:
	cmdTrim := c.Cmd
	if cmdTrim == "cd" || len(cmdTrim) >= 3 && cmdTrim[:3] == "cd " {
		var target string
		if cmdTrim == "cd" {
			target = os.Getenv("HOME")
			if target == "" {
				target = "."
			}
		} else {
			target = cmdTrim[3:]
		}
		// resolve relative to currentDir
		newPath := target
		if !filepath.IsAbs(newPath) {
			newPath = filepath.Join(currentDir, newPath)
		}
		// canonicalize
		if abs, err := filepath.Abs(newPath); err == nil {
			// check if directory exists
			if info, err := os.Stat(abs); err == nil && info.IsDir() {
				currentDir = abs
				saveState()
				postResult(c.ID, fmt.Sprintf("Changed directory to %s", currentDir))
				return
			}
		}
		postResult(c.ID, fmt.Sprintf("cd failed: %s (not a directory)", target))
		return
	}

	// general command: execute in currentDir
	out := execInDir(c.Cmd, currentDir)
	postResult(c.ID, out)
}

func execInDir(cmd, dir string) string {
	c := exec.Command("/bin/bash", "-lc", fmt.Sprintf("cd %q && %s", dir, cmd))
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = &out
	_ = c.Run()
	return out.String()
}

func postResult(cmdID, output string) {
	body := map[string]string{
		"device_id": deviceID,
		"secret":    secret,
		"cmd_id":    cmdID,
		"output":    output,
		"cwd":       currentDir,
	}
	b, _ := json.Marshal(body)
	resp, err := http.Post(managerURL+"/result", "application/json", bytes.NewReader(b))
	if err != nil {
		log.Printf("postResult error: %v\n", err)
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}
