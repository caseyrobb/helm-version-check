package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/yaml.v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
)

var (
	helmVersionGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "helm_chart_version_status",
			Help: "Status of Helm chart versions (1 = up-to-date, 0 = outdated)",
		},
		[]string{"application", "chart", "repo_url", "current_version", "latest_version"},
	)
	verboseLogger = log.New(os.Stdout, "DEBUG: ", log.Ldate|log.Ltime|log.Lshortfile)
	infoLogger    = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime)
)

func init() {
	prometheus.MustRegister(helmVersionGauge)
}

func getLatestChartVersion(repoURL, chartName string, verbose bool) (string, error) {
	if verbose {
		verboseLogger.Printf("Fetching index.yaml from %s for chart %s", repoURL, chartName)
	}
	resp, err := http.Get(repoURL + "/index.yaml")
	if err != nil {
		if verbose {
			verboseLogger.Printf("Failed to fetch index.yaml: %v", err)
		}
		return "", err
	}
	defer resp.Body.Close()

	var index struct {
		Entries map[string][]struct {
			Version string `yaml:"version"`
		} `yaml:"entries"`
	}
	if err := yaml.NewDecoder(resp.Body).Decode(&index); err != nil {
		if verbose {
			verboseLogger.Printf("Failed to decode index.yaml: %v", err)
		}
		return "", err
	}

	versions, ok := index.Entries[chartName]
	if !ok || len(versions) == 0 {
		if verbose {
			verboseLogger.Printf("Chart %s not found in repository %s", chartName, repoURL)
		}
		return "", fmt.Errorf("chart %s not found in repository", chartName)
	}

	latest := versions[0].Version
	for _, v := range versions[1:] {
		current, err := semver.NewVersion(latest)
		if err != nil && verbose {
			verboseLogger.Printf("Invalid semver for version %s: %v", latest, err)
		}
		next, err := semver.NewVersion(v.Version)
		if err != nil && verbose {
			verboseLogger.Printf("Invalid semver for version %s: %v", v.Version, err)
		}
		if err == nil && next.GreaterThan(current) {
			latest = v.Version
		}
	}
	if verbose {
		verboseLogger.Printf("Determined latest version for %s: %s", chartName, latest)
	}
	return latest, nil
}

// processHelmSource handles a single Helm source and updates metrics
func processHelmSource(appName string, source map[string]interface{}, verbose bool) {
	helm, helmFound := source["chart"]
	if !helmFound || helm == nil {
		if verbose {
			verboseLogger.Printf("No Helm source found for %s in this source", appName)
		}
		return
	}

	chartName := ""
	repoURL := ""
	chartVersion := ""

	if chart, ok := source["chart"].(string); ok {
		chartName = chart
	}
	if url, ok := source["repoURL"].(string); ok {
		repoURL = url
	}
	if version, ok := source["targetRevision"].(string); ok {
		chartVersion = version
	}

	if verbose {
		verboseLogger.Printf("Extracted: chart=%s, repoURL=%s, version=%s", chartName, repoURL, chartVersion)
	}

	if chartName == "" || repoURL == "" || chartVersion == "" {
		if verbose {
			verboseLogger.Printf("Skipping %s: incomplete Helm data (chart=%s, repoURL=%s, version=%s)",
				appName, chartName, repoURL, chartVersion)
		}
		return
	}

	if !strings.HasSuffix(repoURL, "/") {
		repoURL += "/"
		if verbose {
			verboseLogger.Printf("Normalized repoURL to: %s", repoURL)
		}
	}

	latestVersion, err := getLatestChartVersion(repoURL, chartName, verbose)
	if err != nil {
		infoLogger.Printf("Error getting latest version for %s: %v", chartName, err)
		return
	}

	currentVer, err := semver.NewVersion(chartVersion)
	if err != nil && verbose {
		verboseLogger.Printf("Invalid current version %s: %v", chartVersion, err)
	}
	latestVer, err := semver.NewVersion(latestVersion)
	if err != nil && verbose {
		verboseLogger.Printf("Invalid latest version %s: %v", latestVersion, err)
	}
	status := 0.0
	if err == nil && currentVer.Equal(latestVer) {
		status = 1.0
	}

	helmVersionGauge.WithLabelValues(
		appName,
		chartName,
		repoURL,
		chartVersion,
		latestVersion,
	).Set(status)

	if verbose {
		verboseLogger.Printf("Set metric: app=%s, status=%v", appName, status)
	}

	fmt.Printf("Application: %s\n", appName)
	fmt.Printf("  Chart Name: %s\n", chartName)
	fmt.Printf("  Repository URL: %s\n", repoURL)
	fmt.Printf("  Current Version: %s\n", chartVersion)
	fmt.Printf("  Latest Version: %s\n", latestVersion)
	fmt.Printf("  Up-to-date: %v\n", status == 1.0)
	fmt.Println("---")
}

func main() {
	verbose := false
	if os.Getenv("LOGLEVEL") == "debug" {
		verbose = true
	}
	infoLogger.Printf("Starting helm-app-lister with verbose=%v", verbose)

	// Get namespace from environment variable, default to "argocd"
	namespace := os.Getenv("NAMESPACE")
	if namespace == "" {
		namespace = "argocd"
	}
	infoLogger.Printf("Using namespace: %s", namespace)

	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("Error getting in-cluster config: %v", err)
	}
	if verbose {
		verboseLogger.Println("Successfully obtained in-cluster config")
	}

	clientset, err := dynamic.NewForConfig(config)
	if err != nil {
		log.Fatalf("Error creating dynamic client: %v", err)
	}
	if verbose {
		verboseLogger.Println("Created Kubernetes dynamic client")
	}

	gvr := schema.GroupVersionResource{
		Group:    "argoproj.io",
		Version:  "v1alpha1",
		Resource: "applications",
	}

	go func() {
		if verbose {
			verboseLogger.Println("Starting Prometheus metrics server on :9080")
		}
		http.Handle("/metrics", promhttp.Handler())
		log.Fatal(http.ListenAndServe(":9080", nil))
	}()

	for {
		if verbose {
			verboseLogger.Printf("Listing applications in namespace %s", namespace)
		}
		list, err := clientset.Resource(gvr).Namespace(namespace).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			infoLogger.Printf("Error listing applications in namespace %s: %v", namespace, err)
			if verbose {
				verboseLogger.Printf("Full error details: %v", err)
			}
			time.Sleep(60 * time.Second)
			continue
		}
		if verbose {
			verboseLogger.Printf("Found %d applications", len(list.Items))
		}

		for _, app := range list.Items {
			appName := app.GetName()
			if verbose {
				verboseLogger.Printf("Processing application: %s", appName)
			}

			spec, ok := app.Object["spec"].(map[string]interface{})
			if !ok {
				if verbose {
					verboseLogger.Printf("Skipping %s: spec is not a map or is missing", appName)
				}
				continue
			}

			// Check for single source (spec.source)
			if source, ok := spec["source"].(map[string]interface{}); ok {
				if verbose {
					verboseLogger.Printf("Found single source for %s", appName)
				}
				processHelmSource(appName, source, verbose)
			}

			// Check for multiple sources (spec.sources)
			if sources, ok := spec["sources"].([]interface{}); ok {
				if verbose {
					verboseLogger.Printf("Found %d sources for %s", len(sources), appName)
				}
				for i, src := range sources {
					if sourceMap, ok := src.(map[string]interface{}); ok {
						if verbose {
							verboseLogger.Printf("Processing source #%d for %s", i+1, appName)
						}
						processHelmSource(appName, sourceMap, verbose)
					} else if verbose {
						verboseLogger.Printf("Skipping source #%d for %s: not a map", i+1, appName)
					}
				}
			} else if verbose && spec["source"] == nil {
				verboseLogger.Printf("No sources found for %s", appName)
			}
		}
		if verbose {
			verboseLogger.Println("Completed cycle, sleeping for 60 seconds")
		}
		time.Sleep(60 * time.Second)
	}
}
