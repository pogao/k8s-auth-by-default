package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pogao/k8s-auth-by-default/internal/controller"
	"github.com/pogao/k8s-auth-by-default/internal/webhook"
	ctrl "sigs.k8s.io/controller-runtime"
)

func main() {
	var parameters WebhookParameters

	flag.IntVar(&parameters.port, "port", 8443, "Webhook server port.")
	flag.StringVar(&parameters.certFile, "tlsCertFile", "/etc/webhook/certs/cert.pem", "File containing the x509 Certificate for HTTPS.")
	flag.StringVar(&parameters.keyFile, "tlsKeyFile", "/etc/webhook/certs/key.pem", "File containing the x509 private key to --tlsCertFile.")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	wh := webhook.NewHandler(logger)

	http.HandleFunc("/mutate", wh.HandleAdmission)

	server := &http.Server{
		Addr: fmt.Sprintf(":%d", parameters.port),
	}

	go func() {
		if err := server.ListenAndServeTLS(parameters.certFile, parameters.keyFile); err != nil && err != http.ErrServerClosed {
			logger.Error("Failed to listen and serve webhook", "error", err)
			os.Exit(1)
		}
	}()

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: webhook.RuntimeScheme,
	})
	if err != nil {
		logger.Error("unable to start manager", "error", err)
		os.Exit(1)
	}

	if err = (&controller.IngressReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Logger: logger,
	}).SetupWithManager(mgr); err != nil {
		logger.Error("unable to create controller", "error", err)
		os.Exit(1)
	}

	go func() {
		logger.Info("starting manager")
		if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
			logger.Error("problem running manager", "error", err)
			os.Exit(1)
		}
	}()

	logger.Info("Server started", "port", parameters.port)

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	<-signalChan

	logger.Info("Got OS shutdown signal, shutting down webhook server gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Error("Error shutting down server", "error", err)
	} else {
		logger.Info("Server shutdown successfully")
	}
}

type WebhookParameters struct {
	port     int
	certFile string
	keyFile  string
}
