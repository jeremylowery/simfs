//go:generate qtc templates/
package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
var fileRE *regexp.Regexp
var pubDir string
var maxUpload int64 = 32 << 20

var faIconMap = map[string]string{
	".asf":  "fa-file-video-o",
	".avi":  "fa-file-video-o",
	".bz2":  "fa-file-archive-o",
	".csv":  "fa-file-excel-o",
	".doc":  "fa-file-word-o",
	".docx": "fa-file-word-o",
	".flv":  "fa-file-video-o",
	".go":   "fa-file-code-o",
	".gz":   "fa-file-archive-o",
	".htm":  "fa-file-code-o",
	".html": "fa-file-code-o",
	".java": "fa-file-code-o",
	".js":   "fa-file-code-o",
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

	if mu := os.Getenv("MAXUPLOAD"); mu != "" {
		mi, err := strconv.Atoi(mu)
		if err != nil {
			maxUpload = int64(mi)
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
	r.PathPrefix("/css/").Handler(http.FileServer(box))
	r.PathPrefix("/img/").Handler(http.FileServer(box))
	r.PathPrefix("/js/").Handler(http.FileServer(box))
	r1 := handlers.CombinedLoggingHandler(os.Stdout, r)
	srv := &http.Server{
		Handler:      r1,
		Addr:         serverAddress(),
		WriteTimeout: 15 * time.Second,
		ReadTimeout:  15 * time.Second}
	log.Printf("listening on %s", serverAddress())
	log.Fatal(srv.ListenAndServe())
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

	r.ParseMultipartForm(maxUpload)
	file, handler, err := r.FormFile("f")
	if err != nil {
		templates.WriteError(w, err)
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
		if readOnlyUser == "" && readWriteUser == "" {
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

		http.Error(w, "Not authorized", 401)
	}
}

// Is this request read only? type-safe context value
func readOnly(r *http.Request) bool {
	v, ok := r.Context().Value(roContext).(bool)
	if !ok {
		return true
	}
	return v
}
