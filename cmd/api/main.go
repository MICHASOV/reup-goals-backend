package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/cors"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/config"
	"reup-goals-backend/internal/db"
	"reup-goals-backend/internal/goals"
	"reup-goals-backend/internal/migrations"
	"reup-goals-backend/internal/privacy"
	"reup-goals-backend/internal/security"
	"reup-goals-backend/internal/subscriptions"
	"reup-goals-backend/internal/tasks"
	"reup-goals-backend/internal/v2/aiactions"
	"reup-goals-backend/internal/v2/aiplatform"
	v2api "reup-goals-backend/internal/v2/api"
	audioapi "reup-goals-backend/internal/v2/audio"
	"reup-goals-backend/internal/v2/bootstrap"
	"reup-goals-backend/internal/v2/contextindex"
	"reup-goals-backend/internal/v2/course"
	"reup-goals-backend/internal/v2/departments"
	"reup-goals-backend/internal/v2/jobs"
	"reup-goals-backend/internal/v2/metrics"
	"reup-goals-backend/internal/v2/operations"
	"reup-goals-backend/internal/v2/profile"
	"reup-goals-backend/internal/v2/strategicmemory"
	"reup-goals-backend/internal/v2/strategy"
	"reup-goals-backend/internal/v2/tactics"
	tasksv2 "reup-goals-backend/internal/v2/tasks"
	"reup-goals-backend/internal/v2/workspacedocs"
)

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatal("Configuration error: ", err)
	}
	jwtSecret := []byte(cfg.JWTSecret)
	secureCookie := cfg.SecureCookies || cfg.Environment == "production" || cfg.Environment == "staging"

	database, err := db.Connect(cfg.ConnString(), db.PoolOptions{
		MaxOpenConns: cfg.DBMaxOpenConns, MaxIdleConns: cfg.DBMaxIdleConns, ConnMaxLifetime: cfg.DBConnMaxLifetime,
	})
	if err != nil {
		log.Fatal("DB error:", err)
	}
	defer database.Close()

	if err := auth.EnsureSchema(database); err != nil {
		log.Fatal("DB migration error:", err)
	}
	if err := subscriptions.EnsureSchema(database); err != nil {
		log.Fatal("DB migration error:", err)
	}
	if err := migrations.Run(database); err != nil {
		log.Fatal("DB migration error:", err)
	}

	rootCtx, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	aiGovernance := aiplatform.NewGovernance(database, aiplatform.Limits{
		RequestsPerMinute: cfg.AIRequestsPerMinute,
		DailyBudgetUSD:    cfg.AIDailyBudgetUSD,
		MonthlyBudgetUSD:  cfg.AIMonthlyBudgetUSD,
	})
	aiClient := ai.New(cfg.OpenAIKey, cfg.OpenAIModel, cfg.OpenAIProxyURL).WithGovernance(aiGovernance)
	auditorAIClient := ai.New(cfg.OpenAIKey, cfg.OpenAIAuditorModel, cfg.OpenAIProxyURL).
		WithMaxOutputTokens(cfg.OpenAIAuditorMaxOutputTokens).
		WithGovernance(aiGovernance)
	advisorAIClient := ai.New(cfg.OpenAIKey, cfg.OpenAIAdvisorModel, cfg.OpenAIProxyURL).
		WithMaxOutputTokens(2600).
		WithGovernance(aiGovernance)
	taskEvaluatorAIClient := ai.New(cfg.OpenAIKey, cfg.OpenAITaskModel, cfg.OpenAIProxyURL).
		WithMaxOutputTokens(900).
		WithGovernance(aiGovernance)
	transcriptionAIClient := ai.New(cfg.OpenAIKey, cfg.OpenAITranscriptionModel, cfg.OpenAIProxyURL).WithGovernance(aiGovernance)
	jobManager := jobs.NewManager(database)
	strategicSourceRecorder := strategicmemory.NewSourceRecorder(database, jobManager)
	taskAI := tasks.New(aiClient, database)
	emailService := auth.NewEmailService(cfg)
	cloudPayments := subscriptions.NewCloudPaymentsClient(cfg)
	subscriptionHandler := subscriptions.NewHandler(database, cloudPayments)
	audioHandler := audioapi.NewHandler(database, transcriptionAIClient)
	aiActionsHandler := aiactions.NewHandler(database)
	aiPlatformHandler := aiplatform.NewHandler(database, cfg.AIAdminKey)
	bootstrapHandler := bootstrap.NewHandler(database)
	courseHandler := course.NewHandler(database)
	departmentHandler := departments.NewHandler(database, strategicSourceRecorder)
	metricsHandler := metrics.NewHandler(database)
	workspaceDocumentsHandler := workspacedocs.NewHandler(database, strategicSourceRecorder)
	workspaceContextIndex := contextindex.New(database, auditorAIClient)
	strategicMemoryHandler := strategicmemory.NewHandler(database, auditorAIClient, cfg.OpenAIAuditorCompactThreshold, jobManager).WithContextIndex(workspaceContextIndex)
	strategyHandler := strategy.NewHandler(database, auditorAIClient, cfg.OpenAIAuditorCompactThreshold, jobManager).WithContextIndex(workspaceContextIndex)
	tacticsHandler := tactics.NewHandler(database, advisorAIClient, cfg.OpenAIAdvisorCompactThreshold, jobManager).WithContextIndex(workspaceContextIndex)
	tasksV2Handler := tasksv2.NewHandler(database, auditorAIClient, taskEvaluatorAIClient, cfg.OpenAIAuditorCompactThreshold, strategicSourceRecorder).WithContextIndex(workspaceContextIndex)
	operationsHandler := operations.NewHandler(database, jobManager)
	privacyHandler := privacy.NewHandler(database)
	profileHandler := profile.NewHandler(database, cfg, emailService, cloudPayments)
	operationsCollector := operations.NewCollector(database, jwtSecret)
	operationsCollector.Start(rootCtx)
	defer operationsCollector.Stop()
	jobManager.Start(rootCtx, cfg.AIJobWorkers)
	defer jobManager.Stop()
	privacy.NewRetentionRunner(database, privacy.RetentionPolicy{
		Interval:        cfg.RetentionInterval,
		AuthCodes:       cfg.AuthCodeRetention,
		HTTPRequestLogs: cfg.HTTPRequestLogRetention,
		ProductEvents:   cfg.ProductEventRetention,
		AICallLogs:      cfg.AICallLogRetention,
		BackgroundJobs:  cfg.BackgroundJobRetention,
		LegalEvidence:   cfg.LegalEvidenceRetention,
		PrivacyRequests: cfg.PrivacyRequestRetention,
	}).Start(rootCtx)

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	// Auth middleware
	mw := auth.New(database, jwtSecret)
	authLimiter := security.NewLimiter(10, time.Minute)

	// -----------------------
	// AUTH (public)
	// -----------------------
	mux.Handle("/auth/register", authLimiter.Wrap(auth.RegisterHandler(database, jwtSecret, emailService, secureCookie, cfg.BrowserAuthOnly)))
	mux.Handle("/auth/login", authLimiter.Wrap(auth.LoginHandler(database, jwtSecret, secureCookie, cfg.BrowserAuthOnly)))
	mux.Handle("/auth/verify-email", authLimiter.Wrap(auth.VerifyEmailHandler(database)))
	mux.Handle("/auth/resend-code", authLimiter.Wrap(auth.ResendCodeHandler(database, emailService)))
	mux.Handle("/auth/forgot-password", authLimiter.Wrap(auth.ForgotPasswordHandler(database, emailService)))
	mux.Handle("/auth/verify-reset-code", authLimiter.Wrap(auth.VerifyResetCodeHandler(database)))
	mux.Handle("/auth/reset-password", authLimiter.Wrap(auth.ResetPasswordHandler(database)))
	mux.Handle("/auth/me", mw.Wrap(auth.MeHandler(database)))
	mux.HandleFunc("/api/v2/privacy/legal-documents", privacyHandler.Documents)
	mux.Handle("/api/v2/privacy/acceptances", v2api.RequireAuth(database, jwtSecret, privacyHandler.Acceptances))
	mux.Handle("/api/v2/privacy/requests", v2api.RequireAuth(database, jwtSecret, privacyHandler.Requests))
	mux.Handle("/api/v2/profile", v2api.RequireAuth(database, jwtSecret, profileHandler.Profile))
	mux.Handle("/api/v2/profile/", v2api.RequireAuth(database, jwtSecret, profileHandler.Profile))

	// -----------------------
	// SUBSCRIPTIONS
	// -----------------------
	mux.Handle("/subscription/status", mw.Wrap(subscriptionHandler.Status))
	mux.Handle("/subscription/checkout-config", mw.Wrap(subscriptionHandler.CheckoutConfig))
	mux.Handle("/subscription/cancel", mw.Wrap(subscriptionHandler.Cancel))

	mux.Handle("/payments/cloudpayments/check", subscriptionHandler.CloudPaymentsWebhook("check"))
	mux.Handle("/payments/cloudpayments/pay", subscriptionHandler.CloudPaymentsWebhook("pay"))
	mux.Handle("/payments/cloudpayments/fail", subscriptionHandler.CloudPaymentsWebhook("fail"))
	mux.Handle("/payments/cloudpayments/recurrent", subscriptionHandler.CloudPaymentsWebhook("recurrent"))
	mux.Handle("/payments/cloudpayments/cancel", subscriptionHandler.CloudPaymentsWebhook("cancel"))

	// -----------------------
	// V2 FOUNDATION
	// -----------------------
	mux.Handle("/api/v2/bootstrap", v2api.RequireAuth(database, jwtSecret, bootstrapHandler.Bootstrap))
	mux.Handle("/api/v2/departments", v2api.RequireAuth(database, jwtSecret, departmentHandler.Departments))
	mux.Handle("/api/v2/departments/", v2api.RequireAuth(database, jwtSecret, departmentHandler.Departments))
	mux.Handle("/api/v2/workspace-documents", v2api.RequireAuth(database, jwtSecret, workspaceDocumentsHandler.Documents))
	mux.Handle("/api/v2/workspace-documents/", v2api.RequireAuth(database, jwtSecret, workspaceDocumentsHandler.Documents))
	mux.Handle("/api/v2/responsibilities", v2api.RequireAuth(database, jwtSecret, departmentHandler.Responsibilities))
	mux.Handle("/api/v2/metrics/catalog", v2api.RequireAuth(database, jwtSecret, metricsHandler.Metrics))
	mux.Handle("/api/v2/metrics/targets", v2api.RequireAuth(database, jwtSecret, metricsHandler.Metrics))
	mux.Handle("/api/v2/metrics/targets/", v2api.RequireAuth(database, jwtSecret, metricsHandler.Metrics))
	mux.Handle("/api/v2/audio/transcriptions", v2api.RequireAuth(database, jwtSecret, audioHandler.Transcriptions))
	mux.Handle("/api/v2/ai-actions", v2api.RequireAuth(database, jwtSecret, aiActionsHandler.Actions))
	mux.Handle("/api/v2/ai-actions/", v2api.RequireAuth(database, jwtSecret, aiActionsHandler.Actions))
	mux.Handle("/api/v2/ai/prompts", v2api.RequireAuth(database, jwtSecret, aiPlatformHandler.Prompts))
	mux.Handle("/api/v2/ai/prompts/", v2api.RequireAuth(database, jwtSecret, aiPlatformHandler.Prompts))
	mux.Handle("/api/v2/ai/usage-policy", v2api.RequireAuth(database, jwtSecret, aiPlatformHandler.UsagePolicy))
	mux.Handle("/api/v2/operations/overview", v2api.RequireAuth(database, jwtSecret, operationsHandler.Overview))
	mux.Handle("/api/v2/strategic-director/messages", v2api.RequireAuth(database, jwtSecret, strategicMemoryHandler.StrategicDirector))
	mux.Handle("/api/v2/strategic-director/state", v2api.RequireAuth(database, jwtSecret, strategicMemoryHandler.StrategicDirector))
	mux.Handle("/api/v2/strategic-director/files", v2api.RequireAuth(database, jwtSecret, strategicMemoryHandler.StrategicDirector))
	if cfg.EnableAIBenchmark {
		mux.Handle("/api/v2/strategic-director/model-benchmark", v2api.RequireAuth(database, jwtSecret, strategicMemoryHandler.ModelBenchmark))
	}
	mux.Handle("/api/v2/strategic-memory/snapshot", v2api.RequireAuth(database, jwtSecret, strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/claims", v2api.RequireAuth(database, jwtSecret, strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/claims/", v2api.RequireAuth(database, jwtSecret, strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/agenda", v2api.RequireAuth(database, jwtSecret, strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/documents", v2api.RequireAuth(database, jwtSecret, strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/documents/", v2api.RequireAuth(database, jwtSecret, strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/quality-audit", v2api.RequireAuth(database, jwtSecret, strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/ai-runs", v2api.RequireAuth(database, jwtSecret, strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/reset", v2api.RequireAuth(database, jwtSecret, strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategy/current", v2api.RequireAuth(database, jwtSecret, strategyHandler.Current))
	mux.Handle("/api/v2/strategy-research-requests", v2api.RequireAuth(database, jwtSecret, strategyHandler.ResearchRequests))
	mux.Handle("/api/v2/strategy-research-requests/", v2api.RequireAuth(database, jwtSecret, strategyHandler.ResearchRequests))
	mux.Handle("/api/v2/strategy-versions", v2api.RequireAuth(database, jwtSecret, strategyHandler.Versions))
	mux.Handle("/api/v2/strategy/artifacts/", v2api.RequireAuth(database, jwtSecret, strategyHandler.Artifacts))
	mux.Handle("/api/v2/strategy/", v2api.RequireAuth(database, jwtSecret, strategyHandler.Strategy))
	mux.Handle("/api/v2/strategy-facilitator/state", v2api.RequireAuth(database, jwtSecret, strategyHandler.Facilitator))
	mux.Handle("/api/v2/strategy-facilitator/messages", v2api.RequireAuth(database, jwtSecret, strategyHandler.Facilitator))
	mux.Handle("/api/v2/strategy-facilitator/files", v2api.RequireAuth(database, jwtSecret, strategyHandler.Facilitator))
	mux.Handle("/api/v2/strategy-facilitator/synthesis", v2api.RequireAuth(database, jwtSecret, strategyHandler.Facilitator))
	mux.Handle("/api/v2/strategy-facilitator/readiness", v2api.RequireAuth(database, jwtSecret, strategyHandler.Facilitator))
	mux.Handle("/api/v2/course/current", v2api.RequireAuth(database, jwtSecret, courseHandler.Current))
	mux.Handle("/api/v2/course/", v2api.RequireAuth(database, jwtSecret, courseHandler.Course))
	mux.Handle("/api/v2/tactics/current", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Current))
	mux.Handle("/api/v2/tactics-facilitator/state", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Facilitator))
	mux.Handle("/api/v2/tactics-facilitator/messages", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Facilitator))
	mux.Handle("/api/v2/tactics-facilitator/actions/apply", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Facilitator))
	mux.Handle("/api/v2/tactics-facilitator/files", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Facilitator))
	mux.Handle("/api/v2/tactics-facilitator/readiness", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Facilitator))
	mux.Handle("/api/v2/tactics-advisor/threads", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Advisor))
	mux.Handle("/api/v2/tactics-advisor/threads/", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Advisor))
	mux.Handle("/api/v2/tactics-advisor/state", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Advisor))
	mux.Handle("/api/v2/tactics-advisor/messages", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Advisor))
	mux.Handle("/api/v2/tactics-advisor/files", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Advisor))
	mux.Handle("/api/v2/tactics/workstreams", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Workstreams))
	mux.Handle("/api/v2/tactics/workstreams/", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Workstreams))
	mux.Handle("/api/v2/tactics/projects", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Projects))
	mux.Handle("/api/v2/tactics/projects/", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Projects))
	mux.Handle("/api/v2/tactics/risks", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Risks))
	mux.Handle("/api/v2/tactics/risks/", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Risks))
	mux.Handle("/api/v2/tactics/hypotheses", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Hypotheses))
	mux.Handle("/api/v2/tactics/hypotheses/", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Hypotheses))
	mux.Handle("/api/v2/tactics/opportunities", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Opportunities))
	mux.Handle("/api/v2/tactics/opportunities/", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Opportunities))
	mux.Handle("/api/v2/tactics/", v2api.RequireAuth(database, jwtSecret, tacticsHandler.Tactics))
	mux.Handle("/api/v2/tasks", v2api.RequireAuth(database, jwtSecret, tasksV2Handler.Tasks))
	mux.Handle("/api/v2/tasks/", v2api.RequireAuth(database, jwtSecret, tasksV2Handler.Tasks))

	// -----------------------
	// GOALS (protected)
	// -----------------------
	mux.Handle("/goal", mw.Wrap(goals.GetGoalHandler(database)))
	mux.Handle("/goal/create", mw.Wrap(goals.CreateGoalHandler(database)))
	mux.Handle("/goal/update", mw.Wrap(goals.UpdateGoalHandler(database)))
	mux.Handle("/goal/reset", mw.Wrap(goals.ResetGoalHandler(database)))

	// -----------------------
	// TASKS (protected)
	// -----------------------
	mux.Handle("/tasks", mw.Wrap(tasks.GetTasksHandler(database)))
	mux.Handle("/task/create", mw.Wrap(tasks.CreateTaskHandler(database, taskAI)))
	mux.Handle("/task/update", mw.Wrap(tasks.UpdateTaskHandler(database, taskAI)))
	mux.Handle("/task/status", mw.Wrap(tasks.SetTaskStatusHandler(database)))
	mux.Handle("/task/clarification/create", mw.Wrap(tasks.CreateTaskClarificationHandler(database, taskAI)))

	// ✅ AUTH (protected actions)
	mux.Handle("/auth/logout", mw.Wrap(auth.LogoutHandler(database, secureCookie)))
	mux.Handle("/auth/delete", mw.Wrap(auth.DeleteAccountHandler(database, strategicMemoryHandler)))

	// AI endpoint (protected)
	mux.Handle("/task/evaluate", mw.Wrap(taskAI.Evaluate))

	allowedOrigins := cfg.CORSAllowedOrigins
	if len(allowedOrigins) == 0 {
		allowedOrigins = []string{"http://localhost:3000", "http://localhost:3002", "http://127.0.0.1:3000", "http://127.0.0.1:3002"}
	}
	corsHandler := cors.New(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowCredentials: true,
		AllowedMethods: []string{
			http.MethodGet,
			http.MethodPost,
			http.MethodPatch,
			http.MethodPut,
			http.MethodDelete,
			http.MethodOptions,
		},
		AllowedHeaders: []string{"Authorization", "Content-Type", "X-Request-ID", "X-AI-Admin-Key"},
		ExposedHeaders: []string{"X-Request-ID", "Server-Timing"},
	}).Handler(mux)
	globalLimiter := security.NewLimiter(300, time.Minute)
	handler := operationsCollector.Middleware(corsHandler)
	handler = security.RequireTrustedOrigin(allowedOrigins, handler)
	handler = globalLimiter.Wrap(handler)
	handler = security.Harden(handler)
	server := &http.Server{
		Addr:              ":8080",
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       cfg.HTTPReadTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		<-rootCtx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP shutdown error: %v", err)
		}
	}()

	log.Println("SERVER RUNNING ON :8080")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("HTTP server error: ", err)
	}
}
