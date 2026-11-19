/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"net/http"
	"os"
	"strconv"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	kueuev1beta2 "sigs.k8s.io/kueue/apis/kueue/v1beta2"

	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/controller"
	decisionengine "github.com/suresh-ls/energyaware-k8s-admission/controller/internal/decision-engine"
	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals"
	carbon "github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals/adapters/carbon"
	"github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals/adapters/cloudspot"
	cost "github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals/adapters/cost"
	demand "github.com/suresh-ls/energyaware-k8s-admission/controller/internal/signals/adapters/demand"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envFloatOrDefault(name string, fallback float64) float64 {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed <= 0 {
		setupLog.Error(err, "Invalid environment value; using default", "variable", name, "value", value, "default", fallback)
		return fallback
	}
	return parsed
}

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kueuev1beta2.AddToScheme(scheme))

	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var webhookPort int
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)

	flag.StringVar(
		&metricsAddr,
		"metrics-bind-address",
		"0",
		"The address the metrics endpoint binds to. "+
			"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.",
	)

	flag.StringVar(
		&probeAddr,
		"health-probe-bind-address",
		":8081",
		"The address the probe endpoint binds to.",
	)

	flag.BoolVar(
		&enableLeaderElection,
		"leader-elect",
		false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.",
	)

	flag.BoolVar(
		&secureMetrics,
		"metrics-secure",
		true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.",
	)

	flag.StringVar(
		&webhookCertPath,
		"webhook-cert-path",
		"",
		"The directory that contains the webhook certificate.",
	)

	flag.StringVar(
		&webhookCertName,
		"webhook-cert-name",
		"tls.crt",
		"The name of the webhook certificate file.",
	)

	flag.StringVar(
		&webhookCertKey,
		"webhook-cert-key",
		"tls.key",
		"The name of the webhook key file.",
	)

	flag.IntVar(
		&webhookPort,
		"webhook-port",
		9443,
		"Port the webhook server listens on. Defaults to 9443. Set -1 to disable the webhook server.",
	)

	flag.StringVar(
		&metricsCertPath,
		"metrics-cert-path",
		"",
		"The directory that contains the metrics server certificate.",
	)

	flag.StringVar(
		&metricsCertName,
		"metrics-cert-name",
		"tls.crt",
		"The name of the metrics server certificate file.",
	)

	flag.StringVar(
		&metricsCertKey,
		"metrics-cert-key",
		"tls.key",
		"The name of the metrics server key file.",
	)

	flag.BoolVar(
		&enableHTTP2,
		"enable-http2",
		false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers",
	)

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Disable HTTP/2 unless explicitly enabled.
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("Disabling HTTP/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// ---------------------------------------------------------------------
	// Webhook setup
	// ---------------------------------------------------------------------

	webhookTLSOpts := tlsOpts

	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
		Port:    webhookPort,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info(
			"Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath,
			"webhook-cert-name", webhookCertName,
			"webhook-cert-key", webhookCertKey,
		)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	// ---------------------------------------------------------------------
	// Metrics setup
	// ---------------------------------------------------------------------

	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		metricsServerOptions.FilterProvider =
			filters.WithAuthenticationAndAuthorization
	}

	if len(metricsCertPath) > 0 {
		setupLog.Info(
			"Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath,
			"metrics-cert-name", metricsCertName,
			"metrics-cert-key", metricsCertKey,
		)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	// ---------------------------------------------------------------------
	// Kubernetes manager
	// ---------------------------------------------------------------------

	mgr, err := ctrl.NewManager(
		ctrl.GetConfigOrDie(),
		ctrl.Options{
			Scheme:                 scheme,
			Metrics:                metricsServerOptions,
			WebhookServer:          webhookServer,
			HealthProbeBindAddress: probeAddr,
			LeaderElection:         enableLeaderElection,
			LeaderElectionID:       "feff8d3b.energygate.io",
		},
	)
	if err != nil {
		setupLog.Error(err, "Failed to start manager")
		os.Exit(1)
	}

	// ---------------------------------------------------------------------
	// Signal providers
	// ---------------------------------------------------------------------

	httpClient := &http.Client{
		Timeout: 15 * time.Second,
	}

	// Carbon provider
	//
	// Electricity Maps configuration:
	//   ELECTRICITY_MAPS_API_TOKEN = API token
	//   ELECTRICITY_MAPS_ZONE      = grid zone, defaults to IN-SO
	//
	// The carbon adapter itself supplies the default API base URL.
	carbonToken := os.Getenv("ELECTRICITY_MAPS_API_TOKEN")
	carbonZone := os.Getenv("ELECTRICITY_MAPS_ZONE")

	carbonClient, err := carbon.NewClient(carbon.Config{
		APIToken:       carbonToken,
		Zone:           carbonZone,
		HTTPClient:     httpClient,
		RequestTimeout: 15 * time.Second,
	})
	if err != nil {
		setupLog.Error(err, "Failed to create Electricity Maps client")
		os.Exit(1)
	}

	carbonProvider := carbon.NewProvider(carbonClient)

	// Cost provider
	//
	// EnergyMap configuration:
	//   ENERGYMAP_API_KEY = API key
	//
	// The cost adapter supplies the default API base URL:
	// https://api.energymap.in
	costAPIKey := os.Getenv("ENERGYMAP_API_KEY")

	costProvider := cost.NewProvider(
		costAPIKey,
		httpClient,
		"",
	)

	// Demand-cap provider
	//
	// Kepler exposes CPU-package power through Prometheus.
	// The production configuration documented for this adapter is:
	//   Prometheus: http://prometheus-kube-prometheus-prometheus.monitoring.svc:9090
	//   Region:     IN-SO
	//   Cap:        10 kW
	region := envOrDefault("ENERGYGATE_REGION", envOrDefault("ELECTRICITY_MAPS_ZONE", "IN-SO"))
	prometheusURL := envOrDefault(
		"ENERGYGATE_PROMETHEUS_URL",
		"http://prometheus-kube-prometheus-prometheus.monitoring.svc:9090",
	)
	demandCapKW := envFloatOrDefault("ENERGYGATE_DEMAND_CAP_KW", 10)

	demandProvider, err := demand.NewProvider(
		demand.Config{
			PrometheusURL: prometheusURL,
			Region:        region,
			CapKW:         demandCapKW,
			MaxSampleAge:  1 * time.Minute,
			Timeout:       5 * time.Second,
		},
	)
	if err != nil {
		setupLog.Error(err, "Failed to create demand-cap provider")
		os.Exit(1)
	}

	// ---------------------------------------------------------------------
	// Provider router
	// ---------------------------------------------------------------------

	providers := map[string]signals.SignalProvider{
		signals.SignalTypeCarbon:    carbonProvider,
		signals.SignalTypeCost:      costProvider,
		signals.SignalTypeDemandCap: demandProvider,
	}
	cloudPool, cloudProvider, err := cloudspot.FromEnvironment(context.Background(), httpClient)
	if err != nil {
		setupLog.Error(err, "Failed to configure cloud spot pricing")
		os.Exit(1)
	}
	if cloudProvider != nil {
		providers[signals.SignalTypeCloudSpot] = cloudProvider
	}
	providerRouter, err := signals.NewProviderRouter(providers)
	if err != nil {
		setupLog.Error(err, "Failed to create signal provider router")
		os.Exit(1)
	}

	// ---------------------------------------------------------------------
	// Generic Signal Handler
	// ---------------------------------------------------------------------

	signalHandler := signals.NewHandler(providerRouter)

	// ---------------------------------------------------------------------
	// Decision Engine
	// ---------------------------------------------------------------------

	decisionConfig, err := decisionengine.ConfigFromEnv(decisionengine.DefaultConfig())
	if err != nil {
		setupLog.Error(err, "Invalid decision-engine configuration")
		os.Exit(1)
	}

	decisionEngine, err := decisionengine.NewEngine(
		signalHandler,
		decisionConfig,
	)
	if err != nil {
		setupLog.Error(err, "Failed to create decision engine")
		os.Exit(1)
	}

	setupLog.Info(
		"Signal pipeline initialized",
		"carbonProvider", "electricity_maps",
		"costProvider", "energymap",
		"demandProvider", "kepler/prometheus",
		"region", region,
		"prometheusURL", prometheusURL,
		"demandCapKW", demandCapKW,
	)

	// ---------------------------------------------------------------------
	// Controllers
	// ---------------------------------------------------------------------

	if err := (&controller.AdmissioncheckControllerReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(
			err,
			"Failed to create controller",
			"controller",
			"admissioncheck-controller",
		)
		os.Exit(1)
	}

	if err := (&controller.WorkloadControllerReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		DecisionEngine: decisionEngine,
		CloudSpotPool:  cloudPool,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(
			err,
			"Failed to create controller",
			"controller",
			"workload-controller",
		)
		os.Exit(1)
	}

	// +kubebuilder:scaffold:builder

	// ---------------------------------------------------------------------
	// Health checks
	// ---------------------------------------------------------------------

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	// ---------------------------------------------------------------------
	// Start manager
	// ---------------------------------------------------------------------

	setupLog.Info("Starting manager")

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}
