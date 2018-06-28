//go:generate qtc templates/
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/GeertJohan/go.rice"
	humanize "github.com/dustin/go-humanize"
	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/jeremylowery/simfs/templates"
	"net/http"
)

type roContextKey string

var readOnlyUser = os.Getenv("ROUSER")
var readWriteUser = os.Getenv("RWUSER")
var roContext = roContextKey("ReadOnly")
var userContext = roContextKey("Username")
var authDBFile = os.Getenv("AUTHDBFILE")
var logFile = os.Getenv("LOGFILE")

var fileRE *regexp.Regexp
var pubDir string

// 16 Megs
var maxMemory int64 = 2 << 23

var faIconMap = map[string]string{
	".asf":  "fa-file-video-o",
	".avi":  "fa-file-video-o",
	".bz2":  "fa-file-archive-o",
	".csv":  "fa-file-excel-o",
	".doc":  "fa-file-word-o",
	".docx": "fa-file-word-o",
	".flv":  "fa-file-video-o",
	".gif":  "fa-file-image-o",
	".go":   "fa-file-code-o",
	".gz":   "fa-file-archive-o",
	".htm":  "fa-file-code-o",
	".html": "fa-file-code-o",
	".java": "fa-file-code-o",
	".js":   "fa-file-code-o",
	".jpeg": "fa-file-image-o",
	".jpg":  "fa-file-image-o",
	".mp3":  "fa-file-audio-o",
	".mp4":  "fa-file-video-o",
	".mpg":  "fa-file-video-o",
	".ods":  "fa-file-excel-o",
	".odt":  "fa-file-word-o",
	".odw":  "fa-file-word-o",
	".pdf":  "fa-file-pdf-o",
	".php":  "fa-file-code-o",
	".pl":   "fa-file-code-o",
	".py":   "fa-file-code-o",
	".rtf":  "fa-file-word-o",
	".txt":  "fa-file-text-o",
	".tgz":  "fa-file-archive-o",
	".xls":  "fa-file-excel-o",
	".xlsx": "fa-file-excel-o",
	".wav":  "fa-file-audio-o",
	".wmv":  "fa-file-video-o",
	".zip":  "fa-file-archive-o",
}

func init() {
	var err error
	pubDir = os.Getenv("PUBDIR")
	if pubDir == "" {
		pubDir, err = os.Getwd()
		if err != nil {
			panic(fmt.Sprintf("No PUBDIR given and could not get current dir: %v", err))
		}
	}

	if mu := os.Getenv("MAXMEM"); mu != "" {
		mi, err := strconv.Atoi(mu)
		if err != nil {
			maxMemory = int64(mi)
		}
	}

	fileRE = regexp.MustCompile("^\\.")

}

func main() {
	r := mux.NewRouter()
	box := rice.MustFindBox("static").HTTPBox()
	r.HandleFunc("/", use(indexHandler, basicAuth))
	r.HandleFunc("/upload", use(uploadHandler, basicAuth))
	r.HandleFunc("/download/{name}", use(downloadHandler, basicAuth))
	r.HandleFunc("/delete/{name}", use(deleteHandler, basicAuth))
	r.PathPrefix("/css/").Handler(http.FileServer(box))
	r.PathPrefix("/img/").Handler(http.FileServer(box))
	r.PathPrefix("/js/").Handler(http.FileServer(box))
	r1 := handlers.CombinedLoggingHandler(os.Stdout, r)
	srv := &http.Server{
		Handler:      r1,
		Addr:         serverAddress(),
		WriteTimeout: time.Hour,
		ReadTimeout:  time.Hour}
	log.Printf("listening on %s", serverAddress())
	go srv.ListenAndServe()
	waitUntilKilled()
}

/* block the current go rountine until a signal is received to shut down the
 * process. To be used from the main goroutine */
func waitUntilKilled() {
	ch := make(chan os.Signal)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	fmt.Fprint(os.Stderr, <-ch, "\n")
	fmt.Fprint(os.Stderr, "Caught signal for shutdown\n")
}

func serverAddress() string {
	portStr := os.Getenv("PORT")
	if len(portStr) == 0 {
		return ":8080"
	} else {
		return ":" + portStr
	}
}

/* The index handler serves up a list of all of the files.
 */
func indexHandler(w http.ResponseWriter, r *http.Request) {
	page := &templates.IndexPage{}
	files, err := ioutil.ReadDir(pubDir)
	if err != nil {
		templates.WriteError(w, err)
		return
	}
	for _, finfo := range files {
		if finfo.IsDir() {
			continue
		}
		if fileRE.Match([]byte(finfo.Name())) {
			continue
		}
		t := templates.FileInfo{Name: finfo.Name()}
		t.Size = humanize.Bytes(uint64(finfo.Size()))
		ext := filepath.Ext(finfo.Name())
		var ok bool
		t.FaIcon, ok = faIconMap[ext]
		if !ok {
			t.FaIcon = "fa-file-o"
		}
		page.Files = append(page.Files, t)
	}
	page.ShowUploadForm = !readOnly(r)
	templates.WritePage(w, page)
}

func downloadHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	fname, ok := vars["name"]
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("No name param in download"))
		return
	}

	path := pubDir + "/" + fname
	http.ServeFile(w, r, path)
}

/* Upload a file to the server */
func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if readOnly(r) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("FORBIDDEN"))
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		w.Write([]byte("NOTALLOWED"))
		return
	}

	r.ParseMultipartForm(maxMemory)
	file, handler, err := r.FormFile("f")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))
		return
	}
	defer file.Close()

	f, err := os.Create(pubDir + "/" + handler.Filename)
	if err != nil {
		templates.WriteError(w, err)
		return
	}
	defer f.Close()
	io.Copy(f, file)
	http.Redirect(w, r, "/", http.StatusFound)
}

func deleteHandler(w http.ResponseWriter, r *http.Request) {
	if readOnly(r) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("FORBIDDEN"))
		return
	}
	vars := mux.Vars(r)
	fname, ok := vars["name"]
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("No name"))
		return
	}

	err := os.Remove(pubDir + "/" + fname)
	if err != nil {
		templates.WriteError(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

// use provides a cleaner interface for chaining middleware for single routes.
// Middleware functions are simple HTTP handlers (w http.ResponseWriter, r *http.Request)
//
//  r.HandleFunc("/login", use(loginHandler, rateLimit, csrf))
//  r.HandleFunc("/form", use(formHandler, csrf))
//  r.HandleFunc("/about", aboutHandler)
//
// See https://gist.github.com/elithrar/7600878#comment-955958 for how to extend it to suit simple http.Handler's
func use(h http.HandlerFunc, middleware ...func(http.HandlerFunc) http.HandlerFunc) http.HandlerFunc {
	for _, m := range middleware {
		h = m(h)
	}

	return h
}

// Leverages nemo's answer in http://stackoverflow.com/a/21937924/556573
func basicAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// If no environmental configuration is set, we serve up the directory
		// as read only by default
		logRequest(r)
		if readOnlyUser == "" && readWriteUser == "" && authDBFile == "" {
			ctx := context.WithValue(r.Context(), roContext, true)
			h.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)

		s := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
		if len(s) != 2 {
			http.Error(w, "Not authorized", 401)
			return
		}

		b, err := base64.StdEncoding.DecodeString(s[1])
		if err != nil {
			http.Error(w, err.Error(), 401)
			return
		}

		pair := strings.SplitN(string(b), ":", 2)
		if len(pair) != 2 {
			http.Error(w, "Not authorized", 401)
			return
		}

		if readOnlyUser != "" && pair[0] == readOnlyUser {
			ctx := context.WithValue(r.Context(), roContext, true)
			h.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		if readWriteUser != "" && pair[0] == readWriteUser {
			ctx := context.WithValue(r.Context(), roContext, false)
			h.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		if authDBFile != "" {
			err, readOnly := fileAuthenticate(pair[0], pair[1])
			if err == nil {
				ctx := context.WithValue(r.Context(), roContext, readOnly)
				h.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}
		http.Error(w, "Not authorized", 401)
	}
}

func logRequest(r *http.Request) {
	if logFile == "" {
		return
	}
	username, _ := requestCredentials(r)
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	var path string
	if r.URL.Path == "/upload" {
		_, handler, _ := r.FormFile("f")
		path = fmt.Sprintf("/upload/%s", handler.Filename)
	} else {
		path = r.URL.Path
	}
	t := time.Now()
	fmt.Fprintf(f, "%-35s %-21s %-20s %s\n",
		t.Format("2006-01-02 15:04:05.999999999 -0700 MST"), r.RemoteAddr, username, path)
	f.Close()

}

func requestCredentials(r *http.Request) (username string, password string) {
	s := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
	if len(s) != 2 {
		return
	}

	b, err := base64.StdEncoding.DecodeString(s[1])
	if err != nil {
		return
	}

	pair := strings.SplitN(string(b), ":", 2)
	if len(pair) != 2 {
		return
	}
	return pair[0], pair[1]

}

func logUserLogin(username string, ip string) {
	if logFile == "" {
		return
	}
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	t := time.Now()
	fmt.Fprintf(f, "%s %-21s %s login\n",
		t.Format("2006-01-02 15:04:05.999999999 -0700 MST"), ip, username)
	f.Close()

}

// Authenticate against the csv file If err is nil then authentication
// successful
func fileAuthenticate(username string, password string) (err error, readOnly bool) {
	f, err := os.Open(authDBFile)
	if err != nil {
		return
	}

	r := csv.NewReader(bufio.NewReader(f))
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if len(record) < 3 {
			continue
		}
		if username != record[0] {
			continue
		}
		if password != record[1] {
			continue
		}
		readOnly = record[2] == "Y" || record[2] == "y"
		return nil, readOnly
	}

	err = errors.New("Invalid username or password")
	return
}

// Is this request read only? type-safe context value
func readOnly(r *http.Request) bool {
	v, ok := r.Context().Value(roContext).(bool)
	if !ok {
		return true
	}
	return v
}
