package resource

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/aws-cloudformation/cloudformation-cli-go-plugin/cfn/handler"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/cloudformation"
	"github.com/google/go-github/v32/github"
	"golang.org/x/oauth2"
)

// https URL regex
var httpsURL string = `^https://[0-9a-zA-Z]([-.\w]*[0-9a-zA-Z])(:[0-9]*)*([?/#].*)?$`

// hookURL regex
var hookURL string = `^https://api\.github\.com/repos/([-\w]+/){2}hooks/\d{9}$`

// makeGitHubClient creates a github.Client object using
// the given oauth token and context. If context is nil,
// a new context is used instead.
func makeGitHubClient(ctx context.Context, token string) *github.Client {
	if ctx == nil {
		ctx = context.Background()
	}

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	return client
}

// parseURL is a copy of the similar function from
// https://github.com/aws-cloudformation/cloudformation-cli-go-plugin/examples/github-repo/cmd/resource/resource.go
// accepts the hook url and returns the hook id, repo and owner
func parseURL(url string) (int64, string, string, error) {
	// example: https://api.github.com/repos/vissree/testbed/hooks/242575190
	// check if url matches the pattern
	matched, _ := regexp.MatchString(hookURL, url)
	if !matched {
		return 0, "", "", fmt.Errorf("Malformed WebhookURL. %v doesn't match %v", url, hookURL)
	}

	parts := strings.Split(url, "/")
	repo, owner := parts[len(parts)-3], parts[len(parts)-4]
	id, _ := strconv.ParseInt(parts[len(parts)-1], 10, 64)

	return id, repo, owner, nil
}

//customFailedEvent creates a custom failure progress event
// based on the handler error code
func customFailedEvent(errorCode, message string) handler.ProgressEvent {
	return handler.ProgressEvent{
		OperationStatus:  handler.Failed,
		HandlerErrorCode: errorCode,
		Message:          message,
	}
}

// createConfig creates a hook config object from the model
func (model *Model) createConfig() (map[string]interface{}, error) {
	config := make(map[string]interface{})

	if model.PayloadURL == nil {
		return nil, fmt.Errorf("Missing required parameter PayloadURL")
	}

	matched, _ := regexp.MatchString(httpsURL, *model.PayloadURL)
	if !matched {
		return nil, fmt.Errorf("Payload URL %s doesn't match %s", *model.PayloadURL, httpsURL)
	}

	config["url"] = *model.PayloadURL

	if model.ContentType == nil {
		// use the default value of json
		config["content_type"] = "json"
	} else {
		if *model.ContentType == "json" || *model.ContentType == "form" {
			config["content_type"] = *model.ContentType
		} else {
			return nil, fmt.Errorf("ContentType must be either json or form")
		}
	}

	if model.Secret != nil {
		config["secret"] = *model.Secret
	}

	if model.InsecureSSL != nil && *model.InsecureSSL {
		config["insecure_ssl"] = "1"
	} else {
		config["insecure_ssl"] = "0"
	}

	return config, nil
}

// createHook creates a hook object from the model
func (model *Model) createHook() (*github.Hook, error) {
	config, err := model.createConfig()
	if err != nil {
		return nil, err
	}

	hook := &github.Hook{Config: config}

	if len(model.Events) > 0 {
		hook.Events = model.Events
	}

	if model.Active != nil {
		hook.Active = model.Active
	}

	return hook, nil
}

// updateModel updates the model object
// with values from the given hook
func (model *Model) updateModel(hook *github.Hook) {
	// Extract values from the Config map if exists
	if len(hook.Config) > 0 {
		payloadURL, ok := hook.Config["url"].(string)
		if ok {
			model.PayloadURL = aws.String(payloadURL)
		}

		contentType, ok := hook.Config["content_type"].(string)
		if ok {
			model.ContentType = aws.String(contentType)
		}

		secret, ok := hook.Config["secret"].(string)
		if ok {
			model.Secret = aws.String(secret)
		}

		insecureSSL, ok := hook.Config["insecure_ssl"].(string)
		if ok {
			if insecureSSL == "0" {
				model.InsecureSSL = aws.Bool(false)
			} else {
				model.InsecureSSL = aws.Bool(true)
			}
		}
	}

	model.Active = aws.Bool(hook.GetActive())
	model.Events = hook.Events
	model.WebhookURL = aws.String(hook.GetURL())
}

// Create handles the Create event from the Cloudformation service.
// https://docs.github.com/en/rest/reference/repos#create-a-repository-webhook
func Create(req handler.Request, prevModel *Model, currentModel *Model) (handler.ProgressEvent, error) {
	if currentModel.Token == nil {
		return customFailedEvent(
			cloudformation.HandlerErrorCodeInvalidRequest,
			"Missing required parameter Token",
		), nil
	}

	if currentModel.Owner == nil {
		return customFailedEvent(
			cloudformation.HandlerErrorCodeInvalidRequest,
			"Missing create only parameter: Owner",
		), nil
	}

	if currentModel.Repo == nil {
		return customFailedEvent(
			cloudformation.HandlerErrorCodeInvalidRequest,
			"Missing create only parameter: Repo",
		), nil
	}

	// Confirm that the readOnlyProperty WebhookURL is not part of the create request
	if currentModel.WebhookURL != nil {
		return customFailedEvent(
			cloudformation.HandlerErrorCodeInvalidRequest,
			"Read only property WebhookURL part of the request",
		), nil
	}

	hookConfig, err := currentModel.createHook()
	if err != nil {
		return customFailedEvent(
			cloudformation.HandlerErrorCodeInvalidRequest,
			err.Error(),
		), nil
	}

	ctx := context.Background()
	client := makeGitHubClient(ctx, *currentModel.Token)
	hook, resp, err := client.Repositories.CreateHook(
		ctx, *currentModel.Owner,
		*currentModel.Repo, hookConfig,
	)

	var errorCode string

	switch resp.StatusCode {
	case 201:
		// Create complete
		currentModel.updateModel(hook)

		return handler.ProgressEvent{
			OperationStatus: handler.Success,
			Message:         "Create complete",
			ResourceModel:   currentModel,
		}, nil
	case 422:
		// If duplicate request
		if strings.Contains(err.Error(), "Hook already exists on this repository") {
			errorCode = cloudformation.HandlerErrorCodeAlreadyExists
		}

		// Invalid input
		errorCode = cloudformation.HandlerErrorCodeInvalidRequest
	case 403:
		// Login attempts exceeded
		errorCode = cloudformation.HandlerErrorCodeServiceLimitExceeded
	case 401:
		// Bad credentials
		errorCode = cloudformation.HandlerErrorCodeAccessDenied
	default:
		errorCode = cloudformation.HandlerErrorCodeServiceInternalError
	}

	return customFailedEvent(errorCode, err.Error()), nil
}

// Read handles the Read event from the Cloudformation service.
// https://docs.github.com/en/rest/reference/repos#get-a-repository-webhook
func Read(req handler.Request, prevModel *Model, currentModel *Model) (handler.ProgressEvent, error) {
	if currentModel.WebhookURL == nil {
		return customFailedEvent(
			cloudformation.HandlerErrorCodeNotFound,
			"Missing primary identifier: WebhookURL",
		), nil
	}

	id, repo, owner, err := parseURL(*currentModel.WebhookURL)
	if err != nil {
		return customFailedEvent(
			cloudformation.HandlerErrorCodeNotFound,
			err.Error(),
		), nil
	}

	ctx := context.Background()
	client := makeGitHubClient(ctx, *currentModel.Token)
	hook, resp, err := client.Repositories.GetHook(ctx, owner, repo, id)

	var errorCode string

	switch resp.StatusCode {
	case 200:
		currentModel.updateModel(hook)
		return handler.ProgressEvent{
			OperationStatus: handler.Success,
			Message:         "Read complete",
			ResourceModel:   currentModel,
		}, nil
	case 403:
		// Login attempts exceeded
		errorCode = cloudformation.HandlerErrorCodeServiceLimitExceeded
	case 401:
		// Bad credentials
		errorCode = cloudformation.HandlerErrorCodeAccessDenied
	case 404:
		// Not Found
		errorCode = cloudformation.HandlerErrorCodeNotFound
	default:
		// Unknown error
		errorCode = cloudformation.HandlerErrorCodeServiceInternalError
	}

	return customFailedEvent(errorCode, err.Error()), nil
}

// Update handles the Update event from the Cloudformation service.
// https://docs.github.com/en/rest/reference/repos#update-a-repository-webhook
func Update(req handler.Request, prevModel *Model, currentModel *Model) (handler.ProgressEvent, error) {
	if *prevModel.Owner != *currentModel.Owner || *prevModel.Repo != *currentModel.Repo || *prevModel.WebhookURL != *currentModel.WebhookURL {
		return customFailedEvent(
			cloudformation.HandlerErrorCodeNotUpdatable,
			"Cannot update create only parameter",
		), nil
	}

	hookConfig, err := currentModel.createHook()
	if err != nil {
		return customFailedEvent(
			cloudformation.HandlerErrorCodeInvalidRequest,
			err.Error(),
		), nil
	}

	id, repo, owner, err := parseURL(*currentModel.WebhookURL)
	if err != nil {
		return customFailedEvent(
			cloudformation.HandlerErrorCodeNotFound,
			err.Error(),
		), nil
	}

	ctx := context.Background()
	client := makeGitHubClient(ctx, *currentModel.Token)
	hook, resp, err := client.Repositories.EditHook(ctx, owner, repo, id, hookConfig)

	var errorCode string

	switch resp.StatusCode {
	case 200:
		currentModel.updateModel(hook)

		return handler.ProgressEvent{
			OperationStatus: handler.Success,
			Message:         "Update complete",
			ResourceModel:   currentModel,
		}, nil
	case 422:
		// Invalid input
		errorCode = cloudformation.HandlerErrorCodeInvalidRequest
	case 403:
		// Login attempts exceeded
		errorCode = cloudformation.HandlerErrorCodeServiceLimitExceeded
	case 401:
		// Bad credentials
		errorCode = cloudformation.HandlerErrorCodeAccessDenied
	case 404:
		// Not Found
		errorCode = cloudformation.HandlerErrorCodeNotFound
	default:
		// Unknown error
		errorCode = cloudformation.HandlerErrorCodeServiceInternalError
	}

	return customFailedEvent(errorCode, err.Error()), nil
}

// Delete handles the Delete event from the Cloudformation service.
// https://docs.github.com/en/rest/reference/repos#delete-a-repository-webhook
func Delete(req handler.Request, prevModel *Model, currentModel *Model) (handler.ProgressEvent, error) {
	if currentModel.WebhookURL == nil {
		return customFailedEvent(
			cloudformation.HandlerErrorCodeNotFound,
			"Missing primary identifier: WebhookURL",
		), nil
	}

	id, repo, owner, err := parseURL(*currentModel.WebhookURL)
	if err != nil {
		return customFailedEvent(
			cloudformation.HandlerErrorCodeNotFound,
			err.Error(),
		), nil
	}

	ctx := context.Background()
	client := makeGitHubClient(ctx, *currentModel.Token)
	resp, err := client.Repositories.DeleteHook(ctx, owner, repo, id)

	var errorCode string

	switch resp.StatusCode {
	case 204:
		return handler.ProgressEvent{
			OperationStatus: handler.Success,
			Message:         "Delete complete",
		}, nil
	case 403:
		// Login attempts exceeded
		errorCode = cloudformation.HandlerErrorCodeServiceLimitExceeded
	case 401:
		// Bad credentials
		errorCode = cloudformation.HandlerErrorCodeAccessDenied
	case 404:
		// Not found
		errorCode = cloudformation.HandlerErrorCodeNotFound
	default:
		// Unknown error
		errorCode = cloudformation.HandlerErrorCodeServiceInternalError
	}

	return customFailedEvent(errorCode, err.Error()), nil
}

// List handles the List event from the Cloudformation service
func List(req handler.Request, prevModel *Model, currentModel *Model) (handler.ProgressEvent, error) {
	// Not implemented, return an empty handler.ProgressEvent
	// and an error
	return handler.ProgressEvent{}, fmt.Errorf("Not implemented: List")
}
