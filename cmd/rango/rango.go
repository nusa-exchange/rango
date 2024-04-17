package main

import (
	"context"
	"crypto/rsa"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/nusa-exchange/rango/pkg/auth"
	"github.com/nusa-exchange/rango/pkg/metrics"
	"github.com/nusa-exchange/rango/pkg/routing"
)

var (
	wsAddr = flag.String("ws-addr", "", "http service address")
	pubKey = flag.String("pubKey", "config/rsa-key.pub", "Path to public key")
	exName = flag.String("exchange", "rango.events", "Exchange name of upstream messages")
)

const prefix = "Bearer "

type httpHanlder func(w http.ResponseWriter, r *http.Request)

func token(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(string(authHeader), prefix) {
		return ""
	}

	return authHeader[len(prefix):]
}

func authHandler(h httpHanlder, key *rsa.PublicKey, mustAuth bool) httpHanlder {
	return func(w http.ResponseWriter, r *http.Request) {
		auth, err := auth.ParseAndValidate(token(r), key)

		if err != nil && mustAuth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		if err == nil {
			r.Header.Set("JwtUID", auth.UID)
			r.Header.Set("JwtRole", auth.Role)
		} else {
			r.Header.Del("JwtUID")
			r.Header.Del("JwtRole")
		}
		h(w, r)
		return
	}
}

func setupLogger() {
	logLevel, ok := os.LookupEnv("LOG_LEVEL")
	if ok {
		level, err := zerolog.ParseLevel(strings.ToLower(logLevel))
		if err != nil {
			panic(err)
		}

		zerolog.SetGlobalLevel(level)
		return
	}

	zerolog.SetGlobalLevel(zerolog.DebugLevel)
}

func getPublicKey() (pub *rsa.PublicKey, err error) {
	ks := auth.KeyStore{}
	encPem := os.Getenv("JWT_PUBLIC_KEY")

	if encPem != "" {
		ks.LoadPublicKeyFromString(encPem)
	} else {
		ks.LoadPublicKeyFromFile(*pubKey)
	}
	if err != nil {
		return nil, err
	}
	if ks.PublicKey == nil {
		return nil, fmt.Errorf("failed")
	}
	return ks.PublicKey, nil
}

func getEnv(name, value string) string {
	v := os.Getenv(name)
	if v == "" {
		return value
	}
	return v
}

func getServerAddress() string {
	if *wsAddr != "" {
		return *wsAddr
	}
	host := getEnv("RANGER_HOST", "0.0.0.0")
	port := getEnv("RANGER_PORT", "8080")
	return fmt.Sprintf("%s:%s", host, port)
}

func getRBACConfig() map[string][]string {
	envs := os.Environ()

	rbacEnv := filterPrefixed("RANGO_RBAC_", envs)

	return envToMatrix(rbacEnv, "RANGO_RBAC_")
}

func envToMatrix(env []string, trimPrefix string) map[string][]string {
	matr := make(map[string][]string)

	for _, rec := range env {
		kv := strings.Split(rec, "=")
		key := strings.ToLower(strings.TrimPrefix(kv[0], trimPrefix))
		value := strings.Split(kv[1], ",")

		matr[key] = value
	}

	return matr
}

func filterPrefixed(prefix string, arr []string) []string {
	var res []string

	for _, rec := range arr {
		if strings.HasPrefix(rec, prefix) {
			res = append(res, rec)
		}
	}

	return res
}

func main() {
	flag.Parse()

	setupLogger()

	metrics.Enable()

	rbac := getRBACConfig()
	hub := routing.NewHub(rbac)
	pub, err := getPublicKey()
	if err != nil {
		log.Error().Msgf("Loading public key failed: %s", err.Error())
		time.Sleep(2 * time.Second)
		return
	}

	kafkaBrokers := strings.Split(os.Getenv("KAFKA_BROKERS"), ",")
	kgoClient, err := kgo.NewClient(
		kgo.SeedBrokers(kafkaBrokers...),
		kgo.ConsumerGroup(fmt.Sprintf("rango-%s", uuid.NewString())),
		kgo.ConsumeTopics(*exName),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		log.Error().Msgf("Failed to create consumer: %s", err.Error())
		return
	}

	log.Info().Msg("Starting rango...")

	go func() {
		for {
			fetches := kgoClient.PollFetches(context.Background())
			for i, fe := range fetches.Errors() {
				log.Error().Msgf("Fetch error %d: %v", i, fe.Err)
			}

			records := fetches.Records()
			for _, r := range records {
				hub.ReceiveMsg(r)

				kgoClient.CommitRecords(context.Background(), r)
			}
		}
	}()

	defer kgoClient.Close()

	go hub.ListenWebsocketEvents()

	wsHandler := func(w http.ResponseWriter, r *http.Request) {
		routing.NewClient(hub, w, r)
	}

	http.HandleFunc("/private", authHandler(wsHandler, pub, true))
	http.HandleFunc("/public", authHandler(wsHandler, pub, false))
	http.HandleFunc("/", authHandler(wsHandler, pub, false))

	go http.ListenAndServe(":4242", promhttp.Handler())

	log.Printf("Listenning on %s", getServerAddress())
	err = http.ListenAndServe(getServerAddress(), nil)
	if err != nil {
		log.Fatal().Msg("ListenAndServe failed: " + err.Error())
	}
}
