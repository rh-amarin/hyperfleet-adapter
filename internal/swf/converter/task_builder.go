package converter

import (
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/serverlessworkflow/sdk-go/v3/model"
)

// BuildHTTPCallTask creates a CallHTTP task for making API requests.
func BuildHTTPCallTask(method, endpointExpr string, headers map[string]string) *model.CallHTTP {
	endpoint := model.NewEndpoint(endpointExpr)

	// If endpoint contains ${, it's a runtime expression
	if isRuntimeExpression(endpointExpr) {
		endpoint = &model.Endpoint{
			RuntimeExpression: model.NewExpr(endpointExpr),
		}
	}

	return &model.CallHTTP{
		Call: "http",
		With: model.HTTPArguments{
			Method:   method,
			Endpoint: endpoint,
			Headers:  headers,
			Output:   "content",
		},
	}
}

// BuildHTTPCallTaskWithExport creates a CallHTTP task with export configuration.
func BuildHTTPCallTaskWithExport(method, endpointExpr string, headers map[string]string, exportExpr string) *model.CallHTTP {
	task := BuildHTTPCallTask(method, endpointExpr, headers)

	if exportExpr != "" {
		task.Export = &model.Export{
			As: model.NewObjectOrRuntimeExpr(exportExpr),
		}
	}

	return task
}

// BuildHTTPCallTaskItem creates a TaskItem wrapping a CallHTTP task.
func BuildHTTPCallTaskItem(name, method, endpointExpr string, headers map[string]string, exportExpr string) *model.TaskItem {
	task := BuildHTTPCallTaskWithExport(method, endpointExpr, headers, exportExpr)
	return &model.TaskItem{
		Key:  name,
		Task: task,
	}
}

// BuildTryWithRetry wraps a task in a try/catch block with retry configuration.
func BuildTryWithRetry(innerTasks model.TaskList, retryAttempts int, catchTasks model.TaskList) *model.TryTask {
	tryTask := &model.TryTask{
		Try: &innerTasks,
		Catch: &model.TryTaskCatch{
			Retry: &model.RetryPolicy{
				Limit: model.RetryLimit{
					Attempt: &model.RetryLimitAttempt{
						Count: retryAttempts,
					},
				},
				Backoff: &model.RetryBackoff{
					Exponential: &model.BackoffDefinition{},
				},
			},
		},
	}

	if len(catchTasks) > 0 {
		tryTask.Catch.Do = &catchTasks
	}

	return tryTask
}

// BuildTryTaskItem creates a TaskItem wrapping a TryTask.
func BuildTryTaskItem(name string, innerTasks model.TaskList, retryAttempts int, catchTasks model.TaskList) *model.TaskItem {
	return &model.TaskItem{
		Key:  name,
		Task: BuildTryWithRetry(innerTasks, retryAttempts, catchTasks),
	}
}

// BuildSetTask creates a SetTask for setting values in the workflow context.
func BuildSetTask(values map[string]interface{}) *model.SetTask {
	return &model.SetTask{
		Set: values,
	}
}

// BuildSetTaskItem creates a TaskItem wrapping a SetTask.
func BuildSetTaskItem(name string, values map[string]interface{}) *model.TaskItem {
	return &model.TaskItem{
		Key:  name,
		Task: BuildSetTask(values),
	}
}

// BuildDoTask creates a DoTask containing nested tasks.
func BuildDoTask(tasks model.TaskList) *model.DoTask {
	return &model.DoTask{
		Do: &tasks,
	}
}

// BuildDoTaskItem creates a TaskItem wrapping a DoTask.
func BuildDoTaskItem(name string, tasks model.TaskList) *model.TaskItem {
	return &model.TaskItem{
		Key:  name,
		Task: BuildDoTask(tasks),
	}
}

// BuildConditionalTask adds an `if` condition to a task.
func BuildConditionalTask(task model.Task, ifExpr string) model.Task {
	if ifExpr == "" {
		return task
	}

	base := task.GetBase()
	if base != nil {
		base.If = model.NewExpr(ifExpr)
	}

	return task
}

// BuildConditionalTaskItem creates a TaskItem with an if condition.
func BuildConditionalTaskItem(name string, task model.Task, ifExpr string) *model.TaskItem {
	conditionalTask := BuildConditionalTask(task, ifExpr)
	return &model.TaskItem{
		Key:  name,
		Task: conditionalTask,
	}
}

// BuildPreconditionHTTPTask builds the HTTP call task for a precondition.
func BuildPreconditionHTTPTask(precond *config_loader.Precondition) *model.TaskItem {
	if precond.APICall == nil {
		return nil
	}

	// Convert Go template URL to jq expression
	url := ConvertGoTemplateToJQ(precond.APICall.URL)

	// Convert headers
	headers := make(map[string]string)
	for _, h := range precond.APICall.Headers {
		headers[h.Name] = ConvertGoTemplateToJQ(h.Value)
	}

	// Build export expression that captures response and evaluates conditions
	exportExpr := BuildPreconditionExportExpr(precond.Name, precond.Capture, precond.Conditions)

	return BuildHTTPCallTaskItem(
		"api",
		precond.APICall.Method,
		url,
		headers,
		exportExpr,
	)
}

// BuildPreconditionCatchSetTask builds the set task for the catch block.
func BuildPreconditionCatchSetTask(precondName string) *model.TaskItem {
	okField := toSnakeCase(precondName) + "_ok"
	return BuildSetTaskItem("fail", map[string]interface{}{
		okField: false,
	})
}

// isRuntimeExpression checks if a string is a runtime expression (${ ... }).
func isRuntimeExpression(s string) bool {
	return len(s) > 3 && s[0:2] == "${" && s[len(s)-1] == '}'
}
