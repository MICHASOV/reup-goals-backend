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
	agentapi "reup-goals-backend/internal/v2/agent"
	"reup-goals-backend/internal/v2/aiactions"
	"reup-goals-backend/internal/v2/aiplatform"
	v2api "reup-goals-backend/internal/v2/api"
	audioapi "reup-goals-backend/internal/v2/audio"
	"reup-goals-backend/internal/v2/billing"
	"reup-goals-backend/internal/v2/bootstrap"
	"reup-goals-backend/internal/v2/contextindex"
	"reup-goals-backend/internal/v2/course"
	"reup-goals-backend/internal/v2/departments"
	"reup-goals-backend/internal/v2/jobs"
	"reup-goals-backend/internal/v2/metrics"
	"reup-goals-backend/internal/v2/navigation"
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
		MaxOpenConns: cfg.DBMaxOpenConns, MaxIdleConns: cfg.DBMaxIdleConns,
		ConnMaxLifetime: cfg.DBConnMaxLifetime, ConnMaxIdleTime: cfg.DBConnMaxIdleTime,
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

	billingService := billing.NewService(database, cfg.BillingEnforcementEnabled)
	billingAdminHandler := billing.NewAdminHandler(billingService, cfg.BillingAdminKey)
	aiGovernance := aiplatform.NewGovernance(database, aiplatform.Limits{
		RequestsPerMinute: cfg.AIRequestsPerMinute,
		DailyBudgetUSD:    cfg.AIDailyBudgetUSD,
		MonthlyBudgetUSD:  cfg.AIMonthlyBudgetUSD,
	}, billingService)
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
	jobManager := jobs.NewManagerWithNamespace(database, cfg.JobQueueNamespace)
	strategicSourceRecorder := strategicmemory.NewSourceRecorder(database, jobManager)
	taskAI := tasks.New(aiClient, database)
	emailService := auth.NewEmailService(cfg)
	cloudPayments := subscriptions.NewCloudPaymentsClient(cfg)
	subscriptionHandler := subscriptions.NewHandler(database, cloudPayments)
	audioHandler := audioapi.NewHandler(database, transcriptionAIClient)
	aiActionsHandler := aiactions.NewHandler(database)
	aiPlatformHandler := aiplatform.NewHandler(database, cfg.AIAdminKey)
	bootstrapHandler := bootstrap.NewHandler(database, cfg.BillingEnforcementEnabled)
	courseHandler := course.NewHandler(database)
	departmentHandler := departments.NewHandler(database, strategicSourceRecorder)
	metricsHandler := metrics.NewHandler(database)
	navigationHandler := navigation.NewHandler(database)
	workspaceDocumentsHandler := workspacedocs.NewHandler(database, strategicSourceRecorder)
	workspaceContextIndex := contextindex.New(database, auditorAIClient)
	workspaceContextIndex.RefreshAllAsync()
	strategicMemoryHandler := strategicmemory.NewHandler(database, auditorAIClient, cfg.OpenAIAuditorCompactThreshold, jobManager).WithContextIndex(workspaceContextIndex)
	strategyHandler := strategy.NewHandler(database, auditorAIClient, cfg.OpenAIAuditorCompactThreshold, jobManager).WithContextIndex(workspaceContextIndex)
	tacticsHandler := tactics.NewHandler(database, advisorAIClient, taskEvaluatorAIClient, cfg.OpenAIAdvisorCompactThreshold, jobManager).WithContextIndex(workspaceContextIndex)
	tasksV2Handler := tasksv2.NewHandler(database, auditorAIClient, taskEvaluatorAIClient, cfg.OpenAIAuditorCompactThreshold, strategicSourceRecorder).WithContextIndex(workspaceContextIndex)
	agentService := agentapi.NewService(
		database,
		agentapi.ServiceConfig{
			Enabled: cfg.AgentRuntimeEnabled, Model: cfg.OpenAIAdvisorModel,
			Secret: cfg.AgentRuntimeSecret, MaxTurns: cfg.AgentRuntimeMaxTurns,
			ReleaseID: cfg.AgentReleaseID,
		},
		agentapi.NewRuntimeClient(cfg.AgentRuntimeURL, cfg.AgentRuntimeSecret),
		jobManager, billingService, workspaceContextIndex, tacticsHandler, strategyHandler,
		workspaceDocumentsHandler, tasksV2Handler,
	)
	agentHandler := agentapi.NewHandler(agentService)
	operationsHandler := operations.NewHandler(database, jobManager)
	privacyHandler := privacy.NewHandler(database)
	profileHandler := profile.NewHandler(database, cfg, emailService, cloudPayments, billingService).
		WithWorkspaceDataCleaner(strategicMemoryHandler)
	operationsCollector := operations.NewCollector(database, jwtSecret)
	operationsCollector.Start(rootCtx)
	defer operationsCollector.Stop()
	jobManager.StartPartitioned(rootCtx, cfg.AIJobWorkers, cfg.AIAgentJobWorkers, agentapi.InteractiveJobPriority)
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
	paidProduct := func(next http.HandlerFunc) http.HandlerFunc {
		return v2api.RequireAuth(database, jwtSecret, v2api.RequireProductAccess(database, next))
	}
	onboardingOrPaid := func(next http.HandlerFunc) http.HandlerFunc {
		return v2api.RequireAuth(database, jwtSecret, v2api.RequireOnboardingOrProductAccess(database, next))
	}

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
	mux.Handle("/auth/register", authLimiter.Wrap(auth.RegisterHandler(database, emailService)))
	mux.Handle("/auth/login", authLimiter.Wrap(auth.LoginHandler(database, jwtSecret, secureCookie, cfg.BrowserAuthOnly)))
	mux.Handle("/auth/verify-email", authLimiter.Wrap(auth.VerifyEmailHandler(database, jwtSecret, secureCookie, cfg.BrowserAuthOnly)))
	mux.Handle("/auth/resend-code", authLimiter.Wrap(auth.ResendCodeHandler(database, emailService)))
	mux.Handle("/auth/forgot-password", authLimiter.Wrap(auth.ForgotPasswordHandler(database, emailService)))
	mux.Handle("/auth/verify-reset-code", authLimiter.Wrap(auth.VerifyResetCodeHandler(database)))
	mux.Handle("/auth/reset-password", authLimiter.Wrap(auth.ResetPasswordHandler(database)))
	mux.Handle("/auth/me", mw.Wrap(auth.MeHandler(database)))
	mux.HandleFunc("/api/v2/privacy/legal-documents", privacyHandler.Documents)
	mux.HandleFunc("/api/v2/invitations/preview", profileHandler.InvitationPreview)
	mux.Handle("/api/v2/privacy/acceptances", v2api.RequireAuth(database, jwtSecret, privacyHandler.Acceptances))
	mux.Handle("/api/v2/privacy/requests", v2api.RequireAuth(database, jwtSecret, privacyHandler.Requests))
	mux.Handle("/api/v2/profile", v2api.RequireAuth(database, jwtSecret, profileHandler.Profile))
	mux.Handle("/api/v2/profile/", v2api.RequireAuth(database, jwtSecret, profileHandler.Profile))
	mux.HandleFunc("/api/v2/admin/billing/invoices/confirm", billingAdminHandler.ConfirmInvoice)

	// -----------------------
	// SUBSCRIPTIONS
	// -----------------------
	mux.Handle("/subscription/status", mw.Wrap(subscriptionHandler.Status))
	mux.Handle("/subscription/cancel", mw.Wrap(subscriptionHandler.Cancel))
	if cfg.BillingPaymentsEnabled {
		mux.Handle("/subscription/checkout-config", mw.Wrap(subscriptionHandler.CheckoutConfig))
		mux.Handle("/payments/cloudpayments/check", subscriptionHandler.CloudPaymentsWebhook("check"))
		mux.Handle("/payments/cloudpayments/pay", subscriptionHandler.CloudPaymentsWebhook("pay"))
		mux.Handle("/payments/cloudpayments/fail", subscriptionHandler.CloudPaymentsWebhook("fail"))
		mux.Handle("/payments/cloudpayments/recurrent", subscriptionHandler.CloudPaymentsWebhook("recurrent"))
		mux.Handle("/payments/cloudpayments/cancel", subscriptionHandler.CloudPaymentsWebhook("cancel"))
	}

	// -----------------------
	// V2 FOUNDATION
	// -----------------------
	mux.Handle("/api/v2/bootstrap", v2api.RequireAuth(database, jwtSecret, bootstrapHandler.Bootstrap))
	mux.Handle("/api/v2/navigation", v2api.RequireAuth(database, jwtSecret, navigationHandler.Navigation))
	mux.Handle("/api/v2/navigation/product-tour", v2api.RequireAuth(database, jwtSecret, navigationHandler.UpdateProductTour))
	mux.Handle("/api/v2/navigation/feature-onboarding", v2api.RequireAuth(database, jwtSecret, navigationHandler.UpdateFeatureOnboarding))
	mux.Handle("/api/v2/onboarding-summary", v2api.RequireAuth(database, jwtSecret, strategicMemoryHandler.OnboardingSummary))
	mux.Handle("/api/v2/departments", paidProduct(departmentHandler.Departments))
	mux.Handle("/api/v2/departments/", paidProduct(departmentHandler.Departments))
	mux.Handle("/api/v2/workspace-documents", paidProduct(workspaceDocumentsHandler.Documents))
	mux.Handle("/api/v2/workspace-documents/", paidProduct(workspaceDocumentsHandler.Documents))
	mux.Handle("/api/v2/responsibilities", paidProduct(departmentHandler.Responsibilities))
	mux.Handle("/api/v2/metrics/catalog", paidProduct(metricsHandler.Metrics))
	mux.Handle("/api/v2/metrics/targets", paidProduct(metricsHandler.Metrics))
	mux.Handle("/api/v2/metrics/targets/", paidProduct(metricsHandler.Metrics))
	mux.Handle("/api/v2/audio/transcriptions", onboardingOrPaid(audioHandler.Transcriptions))
	mux.Handle("/api/v2/ai-actions", paidProduct(aiActionsHandler.Actions))
	mux.Handle("/api/v2/ai-actions/", paidProduct(aiActionsHandler.Actions))
	mux.Handle("/api/v2/ai/prompts", v2api.RequireAuth(database, jwtSecret, aiPlatformHandler.Prompts))
	mux.Handle("/api/v2/ai/prompts/", v2api.RequireAuth(database, jwtSecret, aiPlatformHandler.Prompts))
	mux.Handle("/api/v2/ai/usage-policy", v2api.RequireAuth(database, jwtSecret, aiPlatformHandler.UsagePolicy))
	mux.Handle("/api/v2/operations/overview", v2api.RequireAuth(database, jwtSecret, operationsHandler.Overview))
	mux.Handle("/api/v2/operations/warnings", v2api.RequireAuth(database, jwtSecret, operationsHandler.Warnings))
	mux.Handle("/api/v2/strategic-director/messages", onboardingOrPaid(strategicMemoryHandler.StrategicDirector))
	mux.Handle("/api/v2/strategic-director/state", onboardingOrPaid(strategicMemoryHandler.StrategicDirector))
	mux.Handle("/api/v2/strategic-director/confirm", onboardingOrPaid(strategicMemoryHandler.StrategicDirector))
	mux.Handle("/api/v2/strategic-director/files", onboardingOrPaid(strategicMemoryHandler.StrategicDirector))
	if cfg.EnableAIBenchmark {
		mux.Handle("/api/v2/strategic-director/model-benchmark", paidProduct(strategicMemoryHandler.ModelBenchmark))
	}
	mux.Handle("/api/v2/strategic-memory/snapshot", paidProduct(strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/claims", paidProduct(strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/claims/", paidProduct(strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/agenda", paidProduct(strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/documents", paidProduct(strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/documents/", paidProduct(strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/quality-audit", paidProduct(strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/ai-runs", paidProduct(strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/reset", paidProduct(strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategy/current", paidProduct(strategyHandler.Current))
	mux.Handle("/api/v2/strategy-research-requests", paidProduct(strategyHandler.ResearchRequests))
	mux.Handle("/api/v2/strategy-research-requests/", paidProduct(strategyHandler.ResearchRequests))
	mux.Handle("/api/v2/strategy-versions", paidProduct(strategyHandler.Versions))
	mux.Handle("/api/v2/strategy/artifacts/", paidProduct(strategyHandler.Artifacts))
	mux.Handle("/api/v2/strategy/documents/", paidProduct(strategyHandler.SynthesisDocuments))
	mux.Handle("/api/v2/strategy/", paidProduct(strategyHandler.Strategy))
	mux.Handle("/api/v2/strategy-facilitator/state", paidProduct(strategyHandler.Facilitator))
	mux.Handle("/api/v2/strategy-facilitator/messages", paidProduct(strategyHandler.Facilitator))
	mux.Handle("/api/v2/strategy-facilitator/files", paidProduct(strategyHandler.Facilitator))
	mux.Handle("/api/v2/strategy-facilitator/synthesis", paidProduct(strategyHandler.Facilitator))
	mux.Handle("/api/v2/strategy-facilitator/readiness", paidProduct(strategyHandler.Facilitator))
	mux.Handle("/api/v2/course/current", paidProduct(courseHandler.Current))
	mux.Handle("/api/v2/course/", paidProduct(courseHandler.Course))
	mux.Handle("/api/v2/tactics/current", paidProduct(tacticsHandler.Current))
	mux.Handle("/api/v2/tactics-facilitator/state", paidProduct(tacticsHandler.Facilitator))
	mux.Handle("/api/v2/tactics-facilitator/messages", paidProduct(tacticsHandler.Facilitator))
	mux.Handle("/api/v2/tactics-facilitator/actions/apply", paidProduct(tacticsHandler.Facilitator))
	mux.Handle("/api/v2/tactics-facilitator/files", paidProduct(tacticsHandler.Facilitator))
	mux.Handle("/api/v2/tactics-facilitator/readiness", paidProduct(tacticsHandler.Facilitator))
	mux.Handle("/api/v2/tactics-advisor/threads", paidProduct(tacticsHandler.Advisor))
	mux.Handle("/api/v2/tactics-advisor/threads/", paidProduct(tacticsHandler.Advisor))
	mux.Handle("/api/v2/tactics-advisor/state", paidProduct(tacticsHandler.Advisor))
	mux.Handle("/api/v2/tactics-advisor/messages", paidProduct(tacticsHandler.Advisor))
	mux.Handle("/api/v2/tactics-advisor/files", paidProduct(tacticsHandler.Advisor))
	mux.Handle("/api/v2/advisor/runs", paidProduct(agentHandler.Runs))
	mux.Handle("/api/v2/advisor/runs/", paidProduct(agentHandler.Runs))
	if cfg.AgentRuntimeEnabled {
		mux.HandleFunc("/internal/agent/runs/", agentHandler.InternalEvents)
		mux.HandleFunc("/internal/agent/tools/", agentHandler.InternalTools)
	}
	mux.Handle("/api/v2/tactics/workstreams", paidProduct(tacticsHandler.Workstreams))
	mux.Handle("/api/v2/tactics/workstreams/", paidProduct(tacticsHandler.Workstreams))
	mux.Handle("/api/v2/tactics/projects", paidProduct(tacticsHandler.Projects))
	mux.Handle("/api/v2/tactics/projects/", paidProduct(tacticsHandler.Projects))
	mux.Handle("/api/v2/tactics/evaluations", paidProduct(tacticsHandler.EntityEvaluations))
	mux.Handle("/api/v2/tactics/risks", paidProduct(tacticsHandler.Risks))
	mux.Handle("/api/v2/tactics/risks/", paidProduct(tacticsHandler.Risks))
	mux.Handle("/api/v2/tactics/hypotheses", paidProduct(tacticsHandler.Hypotheses))
	mux.Handle("/api/v2/tactics/hypotheses/", paidProduct(tacticsHandler.Hypotheses))
	mux.Handle("/api/v2/tactics/opportunities", paidProduct(tacticsHandler.Opportunities))
	mux.Handle("/api/v2/tactics/opportunities/", paidProduct(tacticsHandler.Opportunities))
	mux.Handle("/api/v2/tactics/", paidProduct(tacticsHandler.Tactics))
	mux.Handle("/api/v2/tasks", paidProduct(tasksV2Handler.Tasks))
	mux.Handle("/api/v2/tasks/", paidProduct(tasksV2Handler.Tasks))

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
		AllowedHeaders: []string{
			"Authorization", "Content-Type", "X-Request-ID", "X-AI-Admin-Key",
			"X-Billing-Admin-Key",
		},
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
