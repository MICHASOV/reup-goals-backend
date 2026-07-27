package ai

import (
	"context"
	"io"
	"time"
)

type CallMetadata struct {
	WorkspaceID   int
	UserID        int
	Module        string
	PromptName    string
	PromptVersion string
}

type ResolvedCall struct {
	Metadata      CallMetadata
	Instructions  string
	Model         string
	Provider      string
	ReservationID string
}

type CallResult struct {
	ResponseID string
	Usage      Usage
	LatencyMS  int64
	Err        error
}

type Governance interface {
	BeforeCall(context.Context, CallMetadata, string, string) (ResolvedCall, error)
	AfterCall(context.Context, ResolvedCall, CallResult)
}

type Provider interface {
	ModelName() string
	ForModel(string) Provider
	GenerateTextNative(context.Context, string, string, ResponseContextOptions) (TextResult, error)
	GenerateJSONNative(context.Context, string, string, ResponseContextOptions) (TextResult, error)
	UploadFile(context.Context, string, string, io.Reader) (OpenAIFile, error)
	CreateVectorStore(context.Context, string) (OpenAIVectorStore, error)
	AddFileToVectorStore(context.Context, string, string) (OpenAIVectorStoreFile, error)
	WaitVectorStoreFileReady(context.Context, string, string, string, time.Duration) (OpenAIVectorStoreFile, error)
}

// ResourceCleaner is optional so test and alternate providers can implement
// generation without claiming support for persistent external resources.
type ResourceCleaner interface {
	DeleteFile(context.Context, string) error
	DeleteVectorStore(context.Context, string) error
}

type VectorStoreFileCleaner interface {
	DeleteVectorStoreFile(context.Context, string, string) error
}

type ConversationCleaner interface {
	DeleteConversation(context.Context, string) error
}

type callMetadataKey struct{}

func WithCallMetadata(ctx context.Context, metadata CallMetadata) context.Context {
	return context.WithValue(ctx, callMetadataKey{}, metadata)
}

func WithScenario(ctx context.Context, workspaceID int, userID int, module string, promptVersion string) context.Context {
	return WithCallMetadata(ctx, CallMetadata{
		WorkspaceID: workspaceID, UserID: userID, Module: module,
		PromptName: module, PromptVersion: promptVersion,
	})
}

func CallMetadataFromContext(ctx context.Context) CallMetadata {
	metadata, _ := ctx.Value(callMetadataKey{}).(CallMetadata)
	return metadata
}
