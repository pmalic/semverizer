package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
	"golang.org/x/net/idna"
)

const defaultListenPort = 8080

var (
	domain string

	insecure bool

	semVerReplacer = strings.NewReplacer("_lt_", "<", "_gt_", ">", "_lte_", "<=", "_gte_", ">=", "_eq_", "=", "_ne_", "!=", "_a_", "*", "_t_", "~", "_c_", "^")
)

func init() {

	domain = "." + os.Getenv("DOMAIN")

	if domain == "." {
		domain = ".semverizer.io"
	}

	_, insecure = os.LookupEnv("INSECURE")
}

type authzRoundTripper struct {
	authz string
	rt    http.RoundTripper
}

func (art authzRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Add("Authorization", art.authz)
	return art.rt.RoundTrip(r)
}

type tagsListResp struct {
	Tags []string `json:"tags"`
}

func findTag(registryURL *url.URL, authz string, image string, constraint *semver.Constraints) (string, error) {

	var tags []string

	client := &http.Client{Transport: authzRoundTripper{authz: authz, rt: http.DefaultTransport}}
	url := registryURL.String() + "/v2/" + image + "/tags/list?n=1000"

	for {
		rqst, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return "", err
		}

		resp, err := client.Do(rqst)
		if err != nil {
			return "", err
		}

		if resp.StatusCode != 200 {
			return "", errors.New("fetching tags failed: " + resp.Status)
		}

		defer resp.Body.Close()
		jsonDecoder := json.NewDecoder(resp.Body)

		var tagsListResp tagsListResp

		err = jsonDecoder.Decode(&tagsListResp)
		if err != nil {
			return "", err
		}

		tags = append(tags, tagsListResp.Tags...)

		url = resp.Header.Get("Link")
		if url == "" {
			break
		}

		url = strings.Trim(strings.Split(url, ";")[0], "<> ")

		if !strings.HasPrefix(url, "http") {
			url = registryURL.String() + url
		}
	}

	vers := make([]*semver.Version, 0, len(tags))

	for _, tag := range tags {

		ver, err := semver.NewVersion(tag)
		if err != nil {
			continue
		}

		vers = append(vers, ver)
	}

	sort.Sort(semver.Collection(vers))

	for i := len(vers) - 1; i >= 0; i-- {

		ver := vers[i]

		if constraint.Check(ver) {
			return ver.Original(), nil
		}
	}

	return "", errors.New("none of the tags matched")
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(404)
	w.Write([]byte("404 Not Found"))
}

func v2Handler(w http.ResponseWriter, r *http.Request) {

	hostParts := strings.Split(strings.TrimSuffix(strings.Split(r.Host, ":")[0], domain), ".")

	if len(hostParts) == 1 && strings.HasPrefix(hostParts[0], "xn--") {

		host, err := idna.ToUnicode(hostParts[0])
		if err != nil {
			w.WriteHeader(400)
			w.Write([]byte("400 Bad Request"))
			return
		}

		host = strings.Map(func(r rune) rune {
			if (r >= 0x41 && r <= 0x5a) || (r >= 0x61 && r <= 0x7a) || (r >= 0x30 && r <= 0x39) || r == 0x2d {
				return r
			}
			return 0x2e
		}, host)

		hostParts = strings.Split(host, ".")
	}

	var hasSchemeInHost bool
	var scheme string
	var defaultPort int

	switch hostParts[0] {
	case "https":
		hasSchemeInHost = true
		scheme = "https"
		defaultPort = 443
		hostParts = hostParts[1:]

	case "http":
		hasSchemeInHost = true
		scheme = "http"
		defaultPort = 80
		hostParts = hostParts[1:]

	default:
		hasSchemeInHost = false
		scheme = "https"
		defaultPort = 443
	}

	if hasSchemeInHost {

		hostPartsLen := len(hostParts)

		port, err := strconv.Atoi(hostParts[hostPartsLen-1])

		if err == nil {

			hostParts = hostParts[:hostPartsLen-1]

			if port != defaultPort {
				hostParts[hostPartsLen-2] += fmt.Sprintf(":%d", port)
			}
		}
	}

	host := strings.Join(hostParts, ".")

	registryURL, err := url.Parse(scheme + "://" + host)
	if err != nil {
		w.WriteHeader(400)
		w.Write([]byte("400 Bad Request"))
		return
	}

	pathParts := strings.Split(r.URL.Path, "/")
	pathPartsLen := len(pathParts)

	if pathParts[pathPartsLen-2] == "manifests" && !strings.HasPrefix(pathParts[pathPartsLen-1], "sha256:") {

		image := strings.Join(pathParts[2:pathPartsLen-2], "/")
		tag := semVerReplacer.Replace(pathParts[pathPartsLen-1])

		fullRef := host + "/" + image + ":" + tag
		log.Print("DEBUG: SemVer manifest request for " + fullRef)

		constraint, err := semver.NewConstraint(tag)
		if err != nil {
			log.Print("WARNING: could not create a SemVer constraint from tag " + tag)
		} else {
			tag, err := findTag(registryURL, r.Header.Get("Authorization"), image, constraint)
			if err != nil {
				log.Print("ERROR: finding tag for "+fullRef+" failed: ", err)
			} else {
				log.Print("DEBUG: found tag " + tag + " for " + fullRef)

				pathParts[pathPartsLen-1] = tag
				r.URL.Path = strings.Join(pathParts, "/")
			}
		}
	}

	r.URL.Host = registryURL.Host
	r.URL.Scheme = registryURL.Scheme
	r.Host = registryURL.Host

	log.Print("DEBUG: proxying to " + r.URL.String())

	proxy := httputil.NewSingleHostReverseProxy(registryURL)

	proxy.ServeHTTP(w, r)
}

func main() {

	listenPort, err := strconv.Atoi(os.Getenv("PORT"))
	if err != nil {
		listenPort = defaultListenPort
	}

	log.Printf("INFO: listenting on port %d, domain %s", listenPort, domain[1:])

	http.HandleFunc("/", rootHandler)
	http.HandleFunc("/v2/", v2Handler)

	log.Fatal(http.ListenAndServe(":"+strconv.Itoa(listenPort), nil))
}
