package iamMetrics

import (
	"strings"
	"time"

	"controlplane/internal/iam/taxonomy"
	"controlplane/internal/observability"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	authAttemptsCounter       *prometheus.CounterVec
	registerTotalCounter      *prometheus.CounterVec
	loginTotalCounter         *prometheus.CounterVec
	refreshTokenTotalCounter  *prometheus.CounterVec
	refreshReplayTotalCounter *prometheus.CounterVec
	sessionTotalCounter       *prometheus.CounterVec
)

func registerAuthMetrics(registry *prometheus.Registry, namespace string) error {
	authAttemptsCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "auth",
		Name:      "attempts_total",
		Help:      "Authentication attempts by flow and result.",
	}, []string{"flow", "result"})

	registerTotalCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "register_total",
		Help:      "Register flow outcomes by result and cache path.",
	}, []string{"result", "cache_path"})

	loginTotalCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "login_total",
		Help:      "Login flow outcomes by result.",
	}, []string{"result"})

	refreshTokenTotalCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "refresh_token_total",
		Help:      "Refresh token flow outcomes by result.",
	}, []string{"result"})

	refreshReplayTotalCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "auth",
		Name:      "refresh_replay_total",
		Help:      "Detected refresh replay attempts by flow.",
	}, []string{"flow"})

	sessionTotalCounter = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: namespace,
		Subsystem: "iam",
		Name:      "auth_session_total",
		Help:      "User auth session bootstrap outcomes by result.",
	}, []string{"result"})

	for _, collector := range []prometheus.Collector{authAttemptsCounter, registerTotalCounter, loginTotalCounter, refreshTokenTotalCounter, refreshReplayTotalCounter, sessionTotalCounter} {
		if err := registry.Register(collector); err != nil {
			return err
		}
	}
	return nil
}

const (
	registerFlowName     = "register"
	loginFlowName        = "login"
	refreshTokenFlowName = "refresh_token"
)

func ObserveRegisterOutcome(result string, cachePath string) {
	observeAuthAttempt(registerFlowName, result == iamTaxonomy.OutcomeSuccess)
	observeRegister(result, cachePath)
}

func ObserveRegisterDB(operation string, duration time.Duration, err error) {
	if prom := observability.CurrentPrometheus(); prom != nil {
		prom.ObserveDB(registerMetricOperation(operation), duration, err)
	}
}

func ObserveRegisterRedis(operation string, duration time.Duration, err error) {
	if prom := observability.CurrentPrometheus(); prom != nil {
		prom.ObserveRedis(registerMetricOperation(operation), duration, err)
	}
}

func registerMetricOperation(operation string) string {
	operation = strings.TrimSpace(operation)
	if operation == "" {
		operation = "unknown"
	}
	return "iam.register." + operation
}

func ObserveLoginOutcome(result string) {
	observeAuthAttempt(loginFlowName, result == iamTaxonomy.OutcomeSuccess)
	observeLogin(result)
}

func ObserveLoginVerifyMailPublish(published bool, err error, duration time.Duration) {
	if prom := observability.CurrentPrometheus(); prom != nil {
		prom.ObserveRedis("iam.login.publish_verify_mail_job", duration, err)
	}
	observeLogin(iamTaxonomy.LoginOutcomeVerifyMailPublishAttempt)
	if err != nil {
		observeLogin(iamTaxonomy.LoginOutcomeVerifyMailPublishError)
		return
	}
	if published {
		observeLogin(iamTaxonomy.LoginOutcomeVerifyMailPublishSuccess)
		return
	}
	observeLogin(iamTaxonomy.LoginOutcomeVerifyMailPublishDuplicate)
}

func ObserveRefreshTokenOutcome(result string) {
	result = normalizeResult(result)
	observeAuthAttempt(refreshTokenFlowName, result == iamTaxonomy.OutcomeSuccess)
	observeRefreshToken(result)
}

func ObserveRefreshReplay(flow string) {
	if refreshReplayTotalCounter == nil {
		return
	}
	flow = normalizeResult(flow)
	refreshReplayTotalCounter.WithLabelValues(flow).Inc()
}

func ObserveAuthSessionOutcome(result string) {
	if sessionTotalCounter == nil {
		return
	}
	result = normalizeResult(result)
	sessionTotalCounter.WithLabelValues(result).Inc()
}

func observeAuthAttempt(flow string, success bool) {
	if authAttemptsCounter == nil {
		return
	}
	flow = strings.TrimSpace(flow)
	if flow == "" {
		flow = "unknown"
	}
	result := iamTaxonomy.OutcomeFailure
	if success {
		result = iamTaxonomy.OutcomeSuccess
	}
	authAttemptsCounter.WithLabelValues(flow, result).Inc()
}

func observeRegister(result, cachePath string) {
	if registerTotalCounter == nil {
		return
	}
	result = normalizeResult(result)
	cachePath = normalizeResult(cachePath)
	registerTotalCounter.WithLabelValues(result, cachePath).Inc()
}

func observeLogin(result string) {
	if loginTotalCounter == nil {
		return
	}
	result = normalizeResult(result)
	loginTotalCounter.WithLabelValues(result).Inc()
}

func observeRefreshToken(result string) {
	if refreshTokenTotalCounter == nil {
		return
	}
	result = normalizeResult(result)
	refreshTokenTotalCounter.WithLabelValues(result).Inc()
}

func normalizeResult(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return iamTaxonomy.OutcomeUnknown
	}
	return value
}
