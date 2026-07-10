package main

import (
	"log"
	"net/http"

	"github.com/rs/cors"

	"reup-goals-backend/internal/ai"
	"reup-goals-backend/internal/auth"
	"reup-goals-backend/internal/config"
	"reup-goals-backend/internal/db"
	"reup-goals-backend/internal/goals"
	"reup-goals-backend/internal/migrations"
	"reup-goals-backend/internal/subscriptions"
	"reup-goals-backend/internal/tasks"
	v2api "reup-goals-backend/internal/v2/api"
	audioapi "reup-goals-backend/internal/v2/audio"
	"reup-goals-backend/internal/v2/bootstrap"
	"reup-goals-backend/internal/v2/course"
	"reup-goals-backend/internal/v2/knowledge"
	"reup-goals-backend/internal/v2/strategicmemory"
	"reup-goals-backend/internal/v2/strategy"
	"reup-goals-backend/internal/v2/tactics"
	tasksv2 "reup-goals-backend/internal/v2/tasks"
)

func main() {
	cfg := config.Load()
	jwtSecret := []byte(cfg.JWTSecret)

	database, err := db.Connect(cfg.ConnString())
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

	aiClient := ai.New(cfg.OpenAIKey, cfg.OpenAIModel, cfg.OpenAIProxyURL)
	auditorAIClient := ai.New(cfg.OpenAIKey, cfg.OpenAIAuditorModel, cfg.OpenAIProxyURL).
		WithMaxOutputTokens(cfg.OpenAIAuditorMaxOutputTokens)
	transcriptionAIClient := ai.New(cfg.OpenAIKey, cfg.OpenAITranscriptionModel, cfg.OpenAIProxyURL)
	taskAI := tasks.New(aiClient, database)
	emailService := auth.NewEmailService(cfg)
	cloudPayments := subscriptions.NewCloudPaymentsClient(cfg)
	subscriptionHandler := subscriptions.NewHandler(database, cloudPayments)
	audioHandler := audioapi.NewHandler(transcriptionAIClient)
	bootstrapHandler := bootstrap.NewHandler(database)
	courseHandler := course.NewHandler(database)
	knowledgeHandler := knowledge.NewHandler(database)
	strategicMemoryHandler := strategicmemory.NewHandler(database, auditorAIClient, cfg.OpenAIAuditorCompactThreshold)
	strategyHandler := strategy.NewHandler(database)
	tacticsHandler := tactics.NewHandler(database)
	tasksV2Handler := tasksv2.NewHandler(database)

	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	// Auth middleware
	mw := auth.New(jwtSecret)

	// -----------------------
	// AUTH (public)
	// -----------------------
	mux.Handle("/auth/register", auth.RegisterHandler(database, jwtSecret, emailService))
	mux.Handle("/auth/login", auth.LoginHandler(database, jwtSecret))
	mux.Handle("/auth/verify-email", auth.VerifyEmailHandler(database))
	mux.Handle("/auth/resend-code", auth.ResendCodeHandler(database, emailService))
	mux.Handle("/auth/forgot-password", auth.ForgotPasswordHandler(database, emailService))
	mux.Handle("/auth/verify-reset-code", auth.VerifyResetCodeHandler(database))
	mux.Handle("/auth/reset-password", auth.ResetPasswordHandler(database))
	mux.Handle("/auth/me", mw.Wrap(auth.MeHandler(database)))

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
	mux.Handle("/api/v2/bootstrap", v2api.RequireAuth(jwtSecret, bootstrapHandler.Bootstrap))
	mux.Handle("/api/v2/audio/transcriptions", v2api.RequireAuth(jwtSecret, audioHandler.Transcriptions))
	mux.Handle("/api/v2/knowledge-base/blocks", v2api.RequireAuth(jwtSecret, knowledgeHandler.Blocks))
	mux.Handle("/api/v2/knowledge-base/blocks/", v2api.RequireAuth(jwtSecret, knowledgeHandler.Block))
	mux.Handle("/api/v2/strategic-director/messages", v2api.RequireAuth(jwtSecret, strategicMemoryHandler.StrategicDirector))
	mux.Handle("/api/v2/strategic-director/state", v2api.RequireAuth(jwtSecret, strategicMemoryHandler.StrategicDirector))
	mux.Handle("/api/v2/strategic-director/files", v2api.RequireAuth(jwtSecret, strategicMemoryHandler.StrategicDirector))
	if cfg.EnableAIBenchmark {
		mux.Handle("/api/v2/strategic-director/model-benchmark", v2api.RequireAuth(jwtSecret, strategicMemoryHandler.ModelBenchmark))
	}
	mux.Handle("/api/v2/strategic-memory/snapshot", v2api.RequireAuth(jwtSecret, strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/claims", v2api.RequireAuth(jwtSecret, strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/agenda", v2api.RequireAuth(jwtSecret, strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/documents", v2api.RequireAuth(jwtSecret, strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategic-memory/reset", v2api.RequireAuth(jwtSecret, strategicMemoryHandler.StrategicMemory))
	mux.Handle("/api/v2/strategy/current", v2api.RequireAuth(jwtSecret, strategyHandler.Current))
	mux.Handle("/api/v2/strategy/artifacts/", v2api.RequireAuth(jwtSecret, strategyHandler.Artifacts))
	mux.Handle("/api/v2/strategy/", v2api.RequireAuth(jwtSecret, strategyHandler.Strategy))
	mux.Handle("/api/v2/course/current", v2api.RequireAuth(jwtSecret, courseHandler.Current))
	mux.Handle("/api/v2/course/", v2api.RequireAuth(jwtSecret, courseHandler.Course))
	mux.Handle("/api/v2/tactics/current", v2api.RequireAuth(jwtSecret, tacticsHandler.Current))
	mux.Handle("/api/v2/tactics/workstreams", v2api.RequireAuth(jwtSecret, tacticsHandler.Workstreams))
	mux.Handle("/api/v2/tactics/workstreams/", v2api.RequireAuth(jwtSecret, tacticsHandler.Workstreams))
	mux.Handle("/api/v2/tactics/projects", v2api.RequireAuth(jwtSecret, tacticsHandler.Projects))
	mux.Handle("/api/v2/tactics/projects/", v2api.RequireAuth(jwtSecret, tacticsHandler.Projects))
	mux.Handle("/api/v2/tactics/risks", v2api.RequireAuth(jwtSecret, tacticsHandler.Risks))
	mux.Handle("/api/v2/tactics/risks/", v2api.RequireAuth(jwtSecret, tacticsHandler.Risks))
	mux.Handle("/api/v2/tactics/opportunities", v2api.RequireAuth(jwtSecret, tacticsHandler.Opportunities))
	mux.Handle("/api/v2/tactics/opportunities/", v2api.RequireAuth(jwtSecret, tacticsHandler.Opportunities))
	mux.Handle("/api/v2/tactics/", v2api.RequireAuth(jwtSecret, tacticsHandler.Tactics))
	mux.Handle("/api/v2/tasks", v2api.RequireAuth(jwtSecret, tasksV2Handler.Tasks))
	mux.Handle("/api/v2/tasks/", v2api.RequireAuth(jwtSecret, tasksV2Handler.Tasks))

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
	mux.Handle("/auth/logout", mw.Wrap(auth.LogoutHandler()))
	mux.Handle("/auth/delete", mw.Wrap(auth.DeleteAccountHandler(database)))

	// AI endpoint (protected)
	mux.Handle("/task/evaluate", mw.Wrap(taskAI.Evaluate))

	var handler http.Handler
	if len(cfg.CORSAllowedOrigins) > 0 {
		handler = cors.New(cors.Options{
			AllowedOrigins: cfg.CORSAllowedOrigins,
			AllowedMethods: []string{
				http.MethodGet,
				http.MethodPost,
				http.MethodPatch,
				http.MethodPut,
				http.MethodDelete,
				http.MethodOptions,
			},
			AllowedHeaders: []string{"Authorization", "Content-Type"},
		}).Handler(mux)
	} else {
		handler = cors.AllowAll().Handler(mux)
	}

	log.Println("🚀 SERVER RUNNING ON :8080")
	_ = http.ListenAndServe(":8080", handler)
}
