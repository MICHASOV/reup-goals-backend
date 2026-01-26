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
	"reup-goals-backend/internal/tasks"
)

var jwtSecret = []byte("SUPER_SECRET_CHANGE_ME")

func main() {
	cfg := config.Load()

	database, err := db.Connect(cfg.ConnString())
	if err != nil {
		log.Fatal("DB error:", err)
	}
	defer database.Close()

	aiClient := ai.New(cfg.OpenAIKey, cfg.OpenAIModel)
	taskAI := tasks.New(aiClient, database)

	mux := http.NewServeMux()

	// Auth middleware
	mw := auth.New(jwtSecret)

	// -----------------------
	// AUTH (public)
	// -----------------------
	mux.Handle("/auth/register", auth.RegisterHandler(database, jwtSecret))
	mux.Handle("/auth/login", auth.LoginHandler(database, jwtSecret))
	mux.Handle("/auth/me", mw.Wrap(auth.MeHandler(database)))

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

	// AI endpoint (protected)
	mux.Handle("/task/evaluate", mw.Wrap(taskAI.Evaluate))

	handler := cors.AllowAll().Handler(mux)

	log.Println("🚀 SERVER RUNNING ON :8080")
	_ = http.ListenAndServe(":8080", handler)
}
