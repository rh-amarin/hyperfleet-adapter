package converter

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/config_loader"
	"github.com/openshift-hyperfleet/hyperfleet-adapter/internal/swf/tasks"
	"github.com/serverlessworkflow/sdk-go/v3/model"
)

// ConvertAdapterConfig converts a legacy AdapterConfig to a Serverless Workflow model.
// This enables running existing adapter configurations through the SWF engine.
func ConvertAdapterConfig(config *config_loader.AdapterConfig) (*model.Workflow, error) {
	if config == nil {
		return nil, fmt.Errorf("config cannot be nil")
	}

	// Build workflow document
	workflow := &model.Workflow{
		Document: model.Document{
			DSL:       "1.0.0",
			Namespace: "hyperfleet",
			Name:      sanitizeName(config.Metadata.Name),
			Version:   config.Spec.Adapter.Version,
			Title:     fmt.Sprintf("Adapter: %s", config.Metadata.Name),
			Tags:      config.Metadata.Labels,
			Metadata: map[string]interface{}{
				"originalKind":       config.Kind,
				"originalAPIVersion": config.APIVersion,
				"namespace":          config.Metadata.Namespace,
			},
		},
	}

	// Build task list for the 4-phase pipeline
	taskList := make(model.TaskList, 0)

	// Phase 1: Parameter Extraction
	if len(config.Spec.Params) > 0 {
		paramsTask := convertParamsPhase(config.Spec.Params)
		taskList = append(taskList, paramsTask)
	}

	// Phase 2: Preconditions
	var hasPreconditions bool
	if len(config.Spec.Preconditions) > 0 {
		hasPreconditions = true
		preconditionsTask := convertPreconditionsPhase(config.Spec.Preconditions)
		taskList = append(taskList, preconditionsTask)
	}

	// Phase 3: Resources (with conditional on preconditions)
	if len(config.Spec.Resources) > 0 {
		resourcesTask := convertResourcesPhase(config.Spec.Resources, hasPreconditions)
		taskList = append(taskList, resourcesTask)
	}

	// Phase 4: Post-processing
	if config.Spec.Post != nil {
		postTask := convertPostPhase(config.Spec.Post)
		taskList = append(taskList, postTask)
	}

	workflow.Do = &taskList

	return workflow, nil
}

// sanitizeName converts a name to a valid SWF name (hostname_rfc1123 compatible).
func sanitizeName(name string) string {
	if name == "" {
		return "unnamed-workflow"
	}
	// Replace underscores and other invalid characters with dashes
	result := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			result = append(result, c)
		} else if c >= 'A' && c <= 'Z' {
			result = append(result, c+32) // lowercase
		} else {
			result = append(result, '-')
		}
	}
	return string(result)
}

// convertParamsPhase converts the params configuration to native SWF set tasks
// for event and env sources, and hf:k8s-read for secrets/configmaps.
func convertParamsPhase(params []config_loader.Parameter) *model.TaskItem {
	// Separate params by source type
	setValues := make(map[string]interface{})
	var k8sSecrets []map[string]interface{}
	var k8sConfigMaps []map[string]interface{}

	for _, p := range params {
		switch {
		case strings.HasPrefix(p.Source, "event."):
			// Native SWF: use jq expression to extract from event data
			path := strings.TrimPrefix(p.Source, "event.")
			expr := buildJQExpression(".event."+path, p.Default)
			setValues[p.Name] = expr

		case strings.HasPrefix(p.Source, "env."):
			// Native SWF: use jq expression to extract from injected env
			envVar := strings.TrimPrefix(p.Source, "env.")
			expr := buildJQExpression(".env."+envVar, p.Default)
			setValues[p.Name] = expr

		case strings.HasPrefix(p.Source, "secret."):
			// K8s secret: needs hf:k8s-read task
			ref := strings.TrimPrefix(p.Source, "secret.")
			secretDef := map[string]interface{}{
				"name": p.Name,
				"ref":  ref,
			}
			if p.Required {
				secretDef["required"] = true
			}
			if p.Default != nil {
				secretDef["default"] = p.Default
			}
			k8sSecrets = append(k8sSecrets, secretDef)

		case strings.HasPrefix(p.Source, "configmap."):
			// K8s configmap: needs hf:k8s-read task
			ref := strings.TrimPrefix(p.Source, "configmap.")
			cmDef := map[string]interface{}{
				"name": p.Name,
				"ref":  ref,
			}
			if p.Required {
				cmDef["required"] = true
			}
			if p.Default != nil {
				cmDef["default"] = p.Default
			}
			k8sConfigMaps = append(k8sConfigMaps, cmDef)

		default:
			// Unknown source type, try as event path
			expr := buildJQExpression(".event."+p.Source, p.Default)
			setValues[p.Name] = expr
		}
	}

	// If we have only set values (no K8s reads), return a native set task
	if len(k8sSecrets) == 0 && len(k8sConfigMaps) == 0 {
		return &model.TaskItem{
			Key: "extract_params",
			Task: &model.SetTask{
				Set: setValues,
			},
		}
	}

	// If we have K8s reads but no set values, return just hf:k8s-read
	if len(setValues) == 0 {
		with := make(map[string]interface{})
		if len(k8sSecrets) > 0 {
			with["secrets"] = k8sSecrets
		}
		if len(k8sConfigMaps) > 0 {
			with["configmaps"] = k8sConfigMaps
		}
		return &model.TaskItem{
			Key: "load_k8s_params",
			Task: &model.CallFunction{
				Call: tasks.TaskK8sRead,
				With: with,
			},
		}
	}

	// Mixed case: we have both set values and K8s reads
	// For now, return the legacy hf:extract to handle all cases
	// TODO: Return a DoTask with both set and hf:k8s-read tasks in sequence
	sources := make([]map[string]interface{}, 0, len(params))
	for _, p := range params {
		sourceConfig := map[string]interface{}{
			"name":   p.Name,
			"source": p.Source,
		}
		if p.Type != "" {
			sourceConfig["type"] = p.Type
		}
		if p.Required {
			sourceConfig["required"] = true
		}
		if p.Default != nil {
			sourceConfig["default"] = p.Default
		}
		sources = append(sources, sourceConfig)
	}

	return &model.TaskItem{
		Key: "phase_params",
		Task: &model.CallFunction{
			Call: tasks.TaskExtract,
			With: map[string]interface{}{
				"sources": sources,
			},
		},
	}
}

// buildJQExpression creates a jq expression with optional default value.
// Example: buildJQExpression(".env.API_URL", "http://localhost") -> "${ .env.API_URL // \"http://localhost\" }"
func buildJQExpression(path string, defaultVal interface{}) string {
	if defaultVal == nil {
		return fmt.Sprintf("${ %s }", path)
	}

	// Format default value based on type
	switch v := defaultVal.(type) {
	case string:
		// String default: use quotes
		return fmt.Sprintf("${ %s // \"%s\" }", path, v)
	case bool:
		return fmt.Sprintf("${ %s // %t }", path, v)
	case int, int32, int64, float32, float64:
		return fmt.Sprintf("${ %s // %v }", path, v)
	default:
		// For complex types, just use the path without default
		return fmt.Sprintf("${ %s }", path)
	}
}

// convertPreconditionsPhase converts preconditions to native SWF tasks.
// Uses call:http with export for API calls and if conditions for short-circuiting.
func convertPreconditionsPhase(preconditions []config_loader.Precondition) *model.TaskItem {
	if len(preconditions) == 0 {
		return nil
	}

	// Build a list of tasks for the preconditions phase
	taskList := make(model.TaskList, 0, len(preconditions)+1)

	// Collect precondition names for the final evaluation
	var precondNames []string

	for i, p := range preconditions {
		precondNames = append(precondNames, p.Name)

		// Convert each precondition to native SWF task(s)
		precondTask := convertSinglePrecondition(&p, i > 0, precondNames[:i])
		if precondTask != nil {
			taskList = append(taskList, precondTask)
		}
	}

	// Add final evaluation task to compute allMatched and notMetReason
	evaluateTask := buildEvaluateTask(precondNames)
	taskList = append(taskList, evaluateTask)

	// Wrap all tasks in a DoTask
	return &model.TaskItem{
		Key: "phase_preconditions",
		Task: &model.DoTask{
			Do: &taskList,
		},
	}
}

// convertSinglePrecondition converts a single precondition to native SWF task(s).
// If hasIfCondition is true, the task is conditionally executed based on previous preconditions.
func convertSinglePrecondition(p *config_loader.Precondition, hasIfCondition bool, previousPrecondNames []string) *model.TaskItem {
	taskName := toSnakeCase(p.Name)

	// Build if condition based on previous preconditions
	var ifExpr string
	if hasIfCondition && len(previousPrecondNames) > 0 {
		// Only execute if all previous preconditions passed
		ifExpr = "${ " + BuildAllMatchedExpr(previousPrecondNames) + " }"
	}

	// If there's an API call, build HTTP task with optional retry
	if p.APICall != nil {
		return buildPreconditionWithAPICall(p, taskName, ifExpr)
	}

	// If there's only an expression (CEL), use a set task
	if p.Expression != "" {
		return buildPreconditionWithExpression(p, taskName, ifExpr)
	}

	// Fallback: just set _ok to true
	okField := taskName + "_ok"
	setTask := BuildSetTask(map[string]interface{}{
		okField: true,
	})
	if ifExpr != "" {
		setTask.If = model.NewExpr(ifExpr)
	}

	return &model.TaskItem{
		Key:  taskName,
		Task: setTask,
	}
}

// buildPreconditionWithAPICall builds a precondition task that makes an API call.
func buildPreconditionWithAPICall(p *config_loader.Precondition, taskName, ifExpr string) *model.TaskItem {
	// Convert Go template URL to jq expression
	url := ConvertGoTemplateToJQ(p.APICall.URL)

	// Convert headers
	headers := make(map[string]string)
	for _, h := range p.APICall.Headers {
		headers[h.Name] = ConvertGoTemplateToJQ(h.Value)
	}

	// Build export expression
	exportExpr := BuildPreconditionExportExpr(p.Name, p.Capture, p.Conditions)

	// Build the HTTP call task
	httpTask := BuildHTTPCallTaskWithExport(p.APICall.Method, url, headers, exportExpr)

	// If there's retry configuration, wrap in try/catch
	if p.APICall.RetryAttempts > 0 {
		// Build the inner HTTP task
		innerTaskList := model.TaskList{
			&model.TaskItem{Key: "api", Task: httpTask},
		}

		// Build catch block that sets _ok to false
		okField := taskName + "_ok"
		catchTaskList := model.TaskList{
			BuildSetTaskItem("fail", map[string]interface{}{okField: false}),
		}

		tryTask := BuildTryWithRetry(innerTaskList, p.APICall.RetryAttempts, catchTaskList)

		// Add if condition
		if ifExpr != "" {
			tryTask.If = model.NewExpr(ifExpr)
		}

		return &model.TaskItem{
			Key:  taskName,
			Task: tryTask,
		}
	}

	// No retry - just use the HTTP task directly
	if ifExpr != "" {
		httpTask.If = model.NewExpr(ifExpr)
	}

	return &model.TaskItem{
		Key:  taskName,
		Task: httpTask,
	}
}

// buildPreconditionWithExpression builds a precondition task that evaluates a CEL expression.
// Since we're using jq, we'll use hf:cel for complex expressions or convert simple ones.
func buildPreconditionWithExpression(p *config_loader.Precondition, taskName, ifExpr string) *model.TaskItem {
	okField := taskName + "_ok"

	// For simple expressions, try to convert to jq
	// For complex CEL expressions, fall back to hf:cel task
	if isSimpleCELExpression(p.Expression) {
		jqExpr := celToJQ(p.Expression)
		setValues := map[string]interface{}{
			okField: "${ " + jqExpr + " }",
		}
		setTask := BuildSetTask(setValues)
		if ifExpr != "" {
			setTask.If = model.NewExpr(ifExpr)
		}
		return &model.TaskItem{
			Key:  taskName,
			Task: setTask,
		}
	}

	// Fall back to hf:cel for complex expressions
	celTask := &model.CallFunction{
		Call: tasks.TaskCEL,
		With: map[string]interface{}{
			"expression": p.Expression,
			"resultKey":  okField,
		},
	}
	if ifExpr != "" {
		celTask.If = model.NewExpr(ifExpr)
	}

	return &model.TaskItem{
		Key:  taskName,
		Task: celTask,
	}
}

// buildEvaluateTask builds the final evaluation task that computes allMatched and notMetReason.
func buildEvaluateTask(precondNames []string) *model.TaskItem {
	allMatchedExpr := "${ " + BuildAllMatchedExpr(precondNames) + " }"
	notMetReasonExpr := "${ " + BuildNotMetReasonExpr(precondNames) + " }"

	return BuildSetTaskItem("evaluate", map[string]interface{}{
		"allMatched":   allMatchedExpr,
		"notMetReason": notMetReasonExpr,
	})
}

// isSimpleCELExpression checks if a CEL expression can be easily converted to jq.
func isSimpleCELExpression(expr string) bool {
	// Simple expressions are field comparisons like "field == value" or "field != \"\""
	// Complex expressions contain function calls, ternary operators, etc.
	complexPatterns := []string{"?.", ".orValue(", ".filter(", ".map(", ".exists(", ".all("}
	for _, pattern := range complexPatterns {
		if strings.Contains(expr, pattern) {
			return false
		}
	}
	return true
}

// convertPreconditionsPhaseOld is the legacy conversion that uses hf:preconditions.
// Deprecated: Use convertPreconditionsPhase which generates native SWF tasks.
func convertPreconditionsPhaseOld(preconditions []config_loader.Precondition) *model.TaskItem {
	configs := make([]map[string]interface{}, 0, len(preconditions))

	for _, p := range preconditions {
		precondConfig := map[string]interface{}{
			"name": p.Name,
		}

		// Add API call if present
		if p.APICall != nil {
			apiCallConfig := map[string]interface{}{
				"method": p.APICall.Method,
				"url":    p.APICall.URL,
			}
			if p.APICall.Timeout != "" {
				apiCallConfig["timeout"] = p.APICall.Timeout
			}
			if p.APICall.RetryAttempts > 0 {
				apiCallConfig["retryAttempts"] = p.APICall.RetryAttempts
			}
			if p.APICall.RetryBackoff != "" {
				apiCallConfig["retryBackoff"] = p.APICall.RetryBackoff
			}
			if len(p.APICall.Headers) > 0 {
				headers := make(map[string]string)
				for _, h := range p.APICall.Headers {
					headers[h.Name] = h.Value
				}
				apiCallConfig["headers"] = headers
			}
			if p.APICall.Body != "" {
				apiCallConfig["body"] = p.APICall.Body
			}
			precondConfig["apiCall"] = apiCallConfig
		}

		// Add captures if present
		if len(p.Capture) > 0 {
			captures := make([]map[string]interface{}, 0, len(p.Capture))
			for _, c := range p.Capture {
				captureConfig := map[string]interface{}{
					"name": c.Name,
				}
				if c.Field != "" {
					captureConfig["field"] = c.Field
				}
				if c.Expression != "" {
					captureConfig["expression"] = c.Expression
				}
				captures = append(captures, captureConfig)
			}
			precondConfig["capture"] = captures
		}

		// Add conditions if present
		if len(p.Conditions) > 0 {
			conditions := make([]map[string]interface{}, 0, len(p.Conditions))
			for _, c := range p.Conditions {
				conditions = append(conditions, map[string]interface{}{
					"field":    c.Field,
					"operator": c.Operator,
					"value":    c.Value,
				})
			}
			precondConfig["conditions"] = conditions
		}

		// Add expression if present
		if p.Expression != "" {
			precondConfig["expression"] = p.Expression
		}

		configs = append(configs, precondConfig)
	}

	return &model.TaskItem{
		Key: "phase_preconditions",
		Task: &model.CallFunction{
			Call: tasks.TaskPreconditions,
			With: map[string]interface{}{
				"config": configs,
			},
		},
	}
}

// convertResourcesPhase converts resources to an hf:resources task.
// If hasPreconditions is true, adds an `if` condition to skip when preconditions fail.
func convertResourcesPhase(resources []config_loader.Resource, hasPreconditions bool) *model.TaskItem {
	resourceConfigs := make([]map[string]interface{}, 0, len(resources))

	for _, r := range resources {
		resourceConfig := map[string]interface{}{
			"name":     r.Name,
			"manifest": r.Manifest,
		}

		if r.RecreateOnChange {
			resourceConfig["recreateOnChange"] = true
		}

		if r.Discovery != nil {
			discoveryConfig := make(map[string]interface{})
			if r.Discovery.Namespace != "" {
				discoveryConfig["namespace"] = r.Discovery.Namespace
			}
			if r.Discovery.ByName != "" {
				discoveryConfig["byName"] = r.Discovery.ByName
			}
			if r.Discovery.BySelectors != nil && r.Discovery.BySelectors.LabelSelector != nil {
				bySelectors := map[string]interface{}{
					"labelSelector": r.Discovery.BySelectors.LabelSelector,
				}
				discoveryConfig["bySelectors"] = bySelectors
			}
			resourceConfig["discovery"] = discoveryConfig
		}

		resourceConfigs = append(resourceConfigs, resourceConfig)
	}

	resourcesCallTask := &model.CallFunction{
		Call: tasks.TaskResources,
		With: map[string]interface{}{
			"config": resourceConfigs,
		},
	}

	// If there are preconditions, add an `if` condition to skip when allMatched is false
	if hasPreconditions {
		resourcesCallTask.If = model.NewExpr("${ .allMatched == true }")
	}

	return &model.TaskItem{
		Key:  "phase_resources",
		Task: resourcesCallTask,
	}
}

// convertPostPhase converts post-processing configuration to native SWF tasks.
// Uses a hybrid approach:
//   - Payload building: set tasks with jq for simple fields, hf:cel for complex CEL expressions
//   - API calls: native call:http with try/catch for retry
func convertPostPhase(post *config_loader.PostConfig) *model.TaskItem {
	taskList := make(model.TaskList, 0)

	// Step 1: Convert payloads to set tasks
	for _, p := range post.Payloads {
		payloadTask := convertPayloadToSetTask(&p)
		if payloadTask != nil {
			taskList = append(taskList, payloadTask)
		}
	}

	// Step 2: Convert post actions to native tasks
	for _, a := range post.PostActions {
		actionTasks := convertPostActionToTasks(&a)
		taskList = append(taskList, actionTasks...)
	}

	// If no tasks, return nil
	if len(taskList) == 0 {
		return nil
	}

	// Wrap all tasks in a DoTask
	return &model.TaskItem{
		Key: "phase_post",
		Task: &model.DoTask{
			Do: &taskList,
		},
	}
}

// convertPayloadToSetTask converts a payload definition to a set task.
// For complex CEL expressions that can't be converted, falls back to hf:cel.
func convertPayloadToSetTask(p *config_loader.Payload) *model.TaskItem {
	taskName := "build_" + toSnakeCase(p.Name)

	// Get the build definition
	var buildDef interface{}
	if p.Build != nil {
		buildDef = p.Build
	} else if p.BuildRefContent != nil {
		buildDef = p.BuildRefContent
	} else {
		return nil
	}

	// Try to convert the build definition to jq expressions
	jqExpr, canConvert := convertBuildDefToJQ(buildDef)

	if canConvert {
		// Use a set task with jq expression
		return BuildSetTaskItem(taskName, map[string]interface{}{
			p.Name: "${ " + jqExpr + " }",
		})
	}

	// Fall back to hf:cel for complex expressions
	return &model.TaskItem{
		Key: taskName,
		Task: &model.CallFunction{
			Call: tasks.TaskCEL,
			With: map[string]interface{}{
				"build":     buildDef,
				"resultKey": p.Name,
			},
		},
	}
}

// convertBuildDefToJQ attempts to convert a build definition to a jq expression.
// Returns the jq expression and whether conversion was successful.
func convertBuildDefToJQ(buildDef interface{}) (string, bool) {
	switch v := buildDef.(type) {
	case map[string]interface{}:
		return convertMapToJQ(v)
	case string:
		// Simple string - might be a Go template
		return convertGoTemplateToJQExpr(v), true
	default:
		return "", false
	}
}

// convertMapToJQ converts a map build definition to a jq object expression.
func convertMapToJQ(m map[string]interface{}) (string, bool) {
	parts := make([]string, 0, len(m))

	for key, value := range m {
		valueExpr, ok := convertValueToJQ(value)
		if !ok {
			return "", false
		}
		parts = append(parts, fmt.Sprintf("%s: %s", key, valueExpr))
	}

	return "{ " + strings.Join(parts, ", ") + " }", true
}

// convertValueToJQ converts a value to a jq expression.
func convertValueToJQ(value interface{}) (string, bool) {
	switch v := value.(type) {
	case string:
		// Check if it's a Go template
		if strings.Contains(v, "{{") {
			return convertGoTemplateToJQExpr(v), true
		}
		return formatJQValue(v), true

	case map[string]interface{}:
		// Check if this is a field/expression definition
		if _, hasField := v["field"]; hasField {
			expr, ok := BuildPayloadFieldExpr(v)
			return expr, ok
		}
		if _, hasExpr := v["expression"]; hasExpr {
			expr, ok := BuildPayloadFieldExpr(v)
			return expr, ok
		}
		// Nested object
		return convertMapToJQ(v)

	case []interface{}:
		items := make([]string, 0, len(v))
		for _, item := range v {
			itemExpr, ok := convertValueToJQ(item)
			if !ok {
				return "", false
			}
			items = append(items, itemExpr)
		}
		return "[ " + strings.Join(items, ", ") + " ]", true

	case bool, int, int64, float64:
		return fmt.Sprintf("%v", v), true

	case nil:
		return "null", true

	default:
		return "", false
	}
}

// convertGoTemplateToJQExpr converts a Go template string to a jq string expression.
func convertGoTemplateToJQExpr(template string) string {
	// Check if it's purely a template reference like {{ .field }}
	re := regexp.MustCompile(`^\{\{\s*\.([^}]+)\s*\}\}$`)
	if matches := re.FindStringSubmatch(template); len(matches) == 2 {
		fieldPath := strings.TrimSpace(matches[1])
		return ".params." + fieldPath
	}

	// For mixed strings, we need string interpolation
	// jq uses \(.expr) for interpolation inside strings
	converted := ConvertGoTemplateToJQ(template)
	if strings.Contains(converted, "${") {
		// Convert ${ .field } to jq string interpolation
		reJQ := regexp.MustCompile(`\$\{\s*([^}]+)\s*\}`)
		result := reJQ.ReplaceAllString(converted, `\($1)`)
		return `"` + result + `"`
	}

	return formatJQValue(template)
}

// convertPostActionToTasks converts a post action to native SWF tasks.
func convertPostActionToTasks(a *config_loader.PostAction) []*model.TaskItem {
	tasks := make([]*model.TaskItem, 0)

	// Handle API call
	if a.APICall != nil {
		apiTask := convertPostAPICallToTask(a.Name, a.APICall)
		if apiTask != nil {
			tasks = append(tasks, apiTask)
		}
	}

	// Log actions are handled at runtime (no task needed in workflow)
	// They could be converted to set tasks that store log messages if needed

	return tasks
}

// convertPostAPICallToTask converts an API call to a native HTTP task with optional retry.
func convertPostAPICallToTask(name string, apiCall *config_loader.APICall) *model.TaskItem {
	taskName := toSnakeCase(name)

	// Convert URL - handle Go templates
	url := ConvertGoTemplateToJQ(apiCall.URL)

	// Convert headers
	headers := make(map[string]string)
	for _, h := range apiCall.Headers {
		headers[h.Name] = ConvertGoTemplateToJQ(h.Value)
	}

	// Build the HTTP task
	httpTask := &model.CallHTTP{
		Call: "http",
		With: model.HTTPArguments{
			Method:  apiCall.Method,
			Headers: headers,
			Output:  "content",
		},
	}

	// Set endpoint
	if isRuntimeExpression(url) {
		httpTask.With.Endpoint = &model.Endpoint{
			RuntimeExpression: model.NewExpr(url),
		}
	} else {
		httpTask.With.Endpoint = model.NewEndpoint(url)
	}

	// Handle body - convert Go templates
	if apiCall.Body != "" {
		bodyExpr := ConvertGoTemplateToJQ(apiCall.Body)
		// If body is a runtime expression, we need to handle it differently
		if isRuntimeExpression(bodyExpr) {
			// Store the expression reference - will be evaluated at runtime
			httpTask.With.Body = []byte(bodyExpr)
		} else {
			httpTask.With.Body = []byte(apiCall.Body)
		}
	}

	// Add export to capture response
	httpTask.Export = &model.Export{
		As: model.NewObjectOrRuntimeExpr(fmt.Sprintf("${ . + { %s_response: .content, %s_status: .response.statusCode } }", taskName, taskName)),
	}

	// Wrap in try/catch if retry is configured
	if apiCall.RetryAttempts > 0 {
		innerTaskList := model.TaskList{
			&model.TaskItem{Key: "api", Task: httpTask},
		}

		// Build catch block
		catchTaskList := model.TaskList{
			BuildSetTaskItem("error", map[string]interface{}{
				taskName + "_failed": true,
				taskName + "_error":  "${ .error.message // \"API call failed\" }",
			}),
		}

		tryTask := BuildTryWithRetry(innerTaskList, apiCall.RetryAttempts, catchTaskList)

		return &model.TaskItem{
			Key:  taskName,
			Task: tryTask,
		}
	}

	return &model.TaskItem{
		Key:  taskName,
		Task: httpTask,
	}
}

// convertPostPhaseOld is the legacy conversion that uses hf:post.
// Deprecated: Use convertPostPhase which generates native SWF tasks.
func convertPostPhaseOld(post *config_loader.PostConfig) *model.TaskItem {
	postConfig := make(map[string]interface{})

	// Convert payloads
	if len(post.Payloads) > 0 {
		payloads := make([]map[string]interface{}, 0, len(post.Payloads))
		for _, p := range post.Payloads {
			payload := map[string]interface{}{
				"name": p.Name,
			}
			if p.Build != nil {
				payload["build"] = p.Build
			}
			if p.BuildRef != "" {
				payload["buildRef"] = p.BuildRef
			}
			if p.BuildRefContent != nil {
				payload["buildRefContent"] = p.BuildRefContent
			}
			payloads = append(payloads, payload)
		}
		postConfig["payloads"] = payloads
	}

	// Convert post actions
	if len(post.PostActions) > 0 {
		actions := make([]map[string]interface{}, 0, len(post.PostActions))
		for _, a := range post.PostActions {
			action := map[string]interface{}{
				"name": a.Name,
			}

			if a.APICall != nil {
				apiCallConfig := map[string]interface{}{
					"method": a.APICall.Method,
					"url":    a.APICall.URL,
				}
				if a.APICall.Timeout != "" {
					apiCallConfig["timeout"] = a.APICall.Timeout
				}
				if a.APICall.RetryAttempts > 0 {
					apiCallConfig["retryAttempts"] = a.APICall.RetryAttempts
				}
				if a.APICall.RetryBackoff != "" {
					apiCallConfig["retryBackoff"] = a.APICall.RetryBackoff
				}
				if len(a.APICall.Headers) > 0 {
					headers := make(map[string]string)
					for _, h := range a.APICall.Headers {
						headers[h.Name] = h.Value
					}
					apiCallConfig["headers"] = headers
				}
				if a.APICall.Body != "" {
					apiCallConfig["body"] = a.APICall.Body
				}
				action["apiCall"] = apiCallConfig
			}

			if a.Log != nil {
				action["log"] = map[string]interface{}{
					"message": a.Log.Message,
					"level":   a.Log.Level,
				}
			}

			actions = append(actions, action)
		}
		postConfig["postActions"] = actions
	}

	return &model.TaskItem{
		Key: "phase_post",
		Task: &model.CallFunction{
			Call: tasks.TaskPost,
			With: postConfig,
		},
	}
}

// WorkflowFromConfig is a convenience function that converts and returns the workflow.
// Returns an error if conversion fails.
func WorkflowFromConfig(config *config_loader.AdapterConfig) (*model.Workflow, error) {
	return ConvertAdapterConfig(config)
}
