package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/heroku/docker-registry-client/registry"
)

const defaultListenPort = 8080

var (
	domain string

	semVerReplacer = strings.NewReplacer("_lt_", "<", "_gt_", ">", "_lte_", "<=", "_gte_", ">=", "_eq_", "=", "_ne_", "!=", "_a_", "*", "_t_", "~", "_c_", "^")
)

func init() {

	domain = "." + os.Getenv("DOMAIN")

	if domain == "." {
		domain = ".semverizer.io"
	}
}

func findTag(redirectURIBase string, insecure bool, image string, constraint *semver.Constraints) (string, error) {

	var reg *registry.Registry
	var err error

	switch insecure {
	case false:
		reg, err = registry.New(redirectURIBase, "", "")
	case true:
		reg, err = registry.NewInsecure(redirectURIBase, "", "")
	}

	if err != nil {
		return "", err
	}

	tags, err := reg.Tags(image)
	if err != nil {
		return "", err
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
			return ver.String(), nil
		}
	}

	return "", errors.New("none of the tags matched")
}

func rootHandler(w http.ResponseWriter, r *http.Request) {

	if !strings.HasPrefix(r.URL.Path, "/v2/") {
		w.WriteHeader(404)
		w.Write([]byte("404 Not Found"))
		return
	}

	hostParts := strings.Split(strings.TrimSuffix(strings.Split(r.Host, ":")[0], domain), ".")

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

	redirectURIBase := scheme + "://" + host
	redirectURI := redirectURIBase + r.URL.RequestURI()

	pathParts := strings.Split(r.URL.Path, "/")
	pathPartsLen := len(pathParts)

	if pathParts[pathPartsLen-2] == "manifests" && !strings.HasPrefix(pathParts[pathPartsLen-1], "sha256:") {

		image := strings.Join(pathParts[2:pathPartsLen-2], "/")
		tag := semVerReplacer.Replace(pathParts[pathPartsLen-1])

		fullRef := host + "/" + image + ":" + tag
		log.Print("DEBUG: SemVer manifest request for " + fullRef)

		constraint, err := semver.NewConstraint(tag)

		if err != nil {
			log.Print("WARNING: could not create a SemVer constraint from " + tag)
		} else {
			tag, err := findTag(redirectURIBase, scheme == "http", image, constraint)

			if err != nil {
				log.Print("ERROR: finding tag for "+fullRef+" failed: ", err)
			} else {
				log.Print("DEBUG: found tag " + tag + " for " + fullRef)

				pathParts[pathPartsLen-1] = tag
				redirectURI = redirectURIBase + strings.Join(pathParts, "/")
			}
		}
	}

	log.Print("DEBUG: redirecting to " + redirectURI)

	w.Header().Set("Content-Type", "")
	http.Redirect(w, r, redirectURI, 302)
}

func main() {

	listenPort, err := strconv.Atoi(os.Getenv("PORT"))
	if err != nil {
		listenPort = defaultListenPort
	}

	log.Printf("INFO: listenting on port %d, domain %s", listenPort, domain[1:])

	http.HandleFunc("/", rootHandler)
	log.Fatal(http.ListenAndServe(":"+strconv.Itoa(listenPort), nil))
}
