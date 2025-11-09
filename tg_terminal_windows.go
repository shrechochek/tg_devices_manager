package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// --- Types for Telegram responses ---
type UpdateResponse struct {
	OK     bool     `json:"ok"`
	Result []Update `json:"result"`
}

type Update struct {
	UpdateID int64   `json:"update_id"`
	Message  Message `json:"message"`
}

type Message struct {
	MessageID int64     `json:"message_id"`
	From      User      `json:"from"`
	Chat      Chat      `json:"chat"`
	Text      string    `json:"text"`
	Document  *Document `json:"document,omitempty"`
	// Photo omitted for simplicity
}

type User struct {
	ID int64 `json:"id"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
}

type GetFileResp struct {
	OK     bool `json:"ok"`
	Result struct {
		FilePath string `json:"file_path"`
	} `json:"result"`
}

// --- Globals ---
var (
	botToken    string
	allowedID   int64
	passPhrase  string
	apiBase     string
	fileBaseURL string
	currentDir  string

	authenticated = false
)

// --- Helpers ---
func sendMessage(chatID int64, text string) {
	v := url.Values{}
	v.Add("chat_id", strconv.FormatInt(chatID, 10))
	v.Add("text", text)
	resp, err := http.PostForm(apiBase+"sendMessage", v)
	if err != nil {
		fmt.Println("sendMessage error:", err)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	fmt.Println("sendMessage response:", string(b))
}

func getUpdates(offset int64, timeout int) ([]Update, error) {
	u := fmt.Sprintf("%sgetUpdates?timeout=%d", apiBase, timeout)
	if offset > 0 {
		u += fmt.Sprintf("&offset=%d", offset)
	}
	resp, err := http.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var ur UpdateResponse
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&ur); err != nil {
		return nil, err
	}
	if !ur.OK {
		return nil, fmt.Errorf("getUpdates returned ok=false")
	}
	return ur.Result, nil
}

func runCommand(cmd string) string {
	// Use Windows cmd /C
	c := exec.Command("cmd", "/C", cmd)
	c.Dir = currentDir
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = &out
	err := c.Run()
	res := out.String()
	if err != nil {
		res += "\n[error: " + err.Error() + "]"
	}
	if len(res) > 3800 {
		res = res[:3800] + "\n...output truncated..."
	}
	return res
}

func sanitizeFilename(name string) string {
	// keep basename only and replace dangerous chars
	name = filepath.Base(name)
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	out := replacer.Replace(name)
	out = strings.ReplaceAll(out, "..", "_")
	if out == "" {
		out = "file"
	}
	return out
}

func downloadFile(urlStr, dest string) error {
	resp, err := http.Get(urlStr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// create destination (overwrite if exists)
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

// get file_path by file_id (Telegram getFile)
func getFilePath(fileID string) (string, error) {
	resp, err := http.Get(apiBase + "getFile?file_id=" + url.QueryEscape(fileID))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var g GetFileResp
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&g); err != nil {
		return "", err
	}
	if !g.OK {
		return "", fmt.Errorf("getFile returned ok=false")
	}
	return g.Result.FilePath, nil
}

// make unique filename in dir if exists
func makeUniquePath(dir, name string) string {
	clean := sanitizeFilename(name)
	candidate := filepath.Join(dir, clean)
	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate
	}
	ext := filepath.Ext(clean)
	base := strings.TrimSuffix(clean, ext)
	for i := 1; i < 10000; i++ {
		c := filepath.Join(dir, fmt.Sprintf("%s_%d%s", base, i, ext))
		if _, err := os.Stat(c); os.IsNotExist(err) {
			return c
		}
	}
	return candidate + ".new"
}

// --- Main ---
func main() {
	botToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	if botToken == "" {
		fmt.Println("Please set TELEGRAM_BOT_TOKEN")
		return
	}
	idStr := os.Getenv("ALLOWED_USER_ID")
	if idStr == "" {
		fmt.Println("Please set ALLOWED_USER_ID")
		return
	}
	var err error
	allowedID, err = strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		fmt.Println("Bad ALLOWED_USER_ID:", err)
		return
	}
	passPhrase = os.Getenv("PASS_PHRASE")
	if passPhrase == "" {
		fmt.Println("Please set PASS_PHRASE")
		return
	}

	apiBase = "https://api.telegram.org/bot" + botToken + "/"
	fileBaseURL = "https://api.telegram.org/file/bot" + botToken + "/"

	// default current dir -> USERPROFILE or .
	home := os.Getenv("USERPROFILE")
	if home == "" {
		home = os.Getenv("HOME")
	}
	if home == "" {
		home = "."
	}
	currentDir = home

	fmt.Println("Bot started. Allowed user:", allowedID)
	// Send startup message (may fail if user never started chat)
	sendMessage(allowedID, fmt.Sprintf("🟢 Bot started at %s\nCurrent dir: %s", time.Now().Format("2006-01-02 15:04:05"), currentDir))

	var offset int64 = 0

	for {
		updates, err := getUpdates(offset, 30)
		if err != nil {
			fmt.Println("getUpdates error:", err)
			time.Sleep(2 * time.Second)
			continue
		}
		if len(updates) > 0 {
			fmt.Println("Got updates:", len(updates))
		}
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			msg := u.Message
			fmt.Printf("Incoming message from %d: %#v\n", msg.From.ID, msg.Text)

			// Only allowed user
			if msg.From.ID != allowedID {
				fmt.Println("Ignored message from", msg.From.ID)
				continue
			}

			// If not authenticated — expect exact passPhrase message
			if !authenticated {
				if strings.TrimSpace(msg.Text) == passPhrase {
					authenticated = true
					sendMessage(msg.Chat.ID, "✅ Authenticated. Current dir: "+currentDir)
				} else {
					sendMessage(msg.Chat.ID, "❌ Send pass phrase to authenticate.")
				}
				continue
			}

			// Handle document
			if msg.Document != nil {
				fmt.Println("Document received:", msg.Document.FileName, msg.Document.FileID)
				fp, err := getFilePath(msg.Document.FileID)
				if err != nil {
					sendMessage(msg.Chat.ID, "❌ Failed getFile: "+err.Error())
					continue
				}
				downloadURL := fileBaseURL + fp
				dest := makeUniquePath(currentDir, msg.Document.FileName)
				err = downloadFile(downloadURL, dest)
				if err != nil {
					sendMessage(msg.Chat.ID, "❌ Failed to save file: "+err.Error())
				} else {
					sendMessage(msg.Chat.ID, "📁 File saved: "+dest)
				}
				continue
			}

			// If text command
			text := strings.TrimSpace(msg.Text)
			if text == "" {
				continue
			}

			// cd handling
			if strings.HasPrefix(text, "cd ") || text == "cd" {
				var target string
				if text == "cd" {
					target = home
				} else {
					arg := strings.TrimSpace(text[3:])
					// If absolute path -> use it, else join with currentDir
					if filepath.IsAbs(arg) {
						target = arg
					} else {
						target = filepath.Join(currentDir, arg)
					}
				}
				// clean and canonicalize
				cleanTarget := filepath.Clean(target)
				if info, err := os.Stat(cleanTarget); err == nil && info.IsDir() {
					currentDir = cleanTarget
					sendMessage(msg.Chat.ID, "📂 Current dir: "+currentDir)
				} else {
					sendMessage(msg.Chat.ID, "❌ Directory not found or inaccessible: "+cleanTarget)
				}
				continue
			}

			if text == "pwd" {
				sendMessage(msg.Chat.ID, currentDir)
				continue
			}

			// Execute arbitrary command using cmd /C
			out := runCommand(text)
			sendMessage(msg.Chat.ID, out)
		}
		// small sleep to avoid tight loop when no updates
		time.Sleep(500 * time.Millisecond)
	}
}
