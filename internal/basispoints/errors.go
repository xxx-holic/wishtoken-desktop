package basispoints

import (
	"errors"
	"fmt"
	"strings"
)

// Categories classify preparation failures without leaking caller content.
const (
	CategoryToolHistory  = "tool_history"
	CategoryImageInput   = "image_input"
	CategoryImageLimit   = "image_limit"
	CategoryToolChoice   = "tool_choice"
	CategoryToolCatalog  = "tool_catalog"
	CategoryHistoryRef   = "history_reference"
	CategoryReasoning    = "reasoning_configuration"
	CategoryOutputFormat = "output_format"
	CategoryModel        = "model"
	CategoryRequestJSON  = "request_json"
	CategoryRequestShape = "request_shape"
)

// PrepareError is a request the gateway cannot serve.
type PrepareError struct {
	Category string
	Detail   string
}

func (e *PrepareError) Error() string { return e.Detail }

func prepareErr(category, format string, args ...any) error {
	return &PrepareError{Category: category, Detail: fmt.Sprintf(format, args...)}
}

// Category returns the classification of err.
func Category(err error) string {
	var pe *PrepareError
	if errors.As(err, &pe) {
		return pe.Category
	}
	if err == nil {
		return ""
	}
	return CategoryRequestShape
}

var userMessages = map[string]string{
	CategoryToolHistory:  "工具调用历史不完整：本次请求里的工具结果找不到对应的原始工具调用（重启、换号或会话过长会让缓存失效），请新建会话后重试。 / Tool history is incomplete; start a new conversation.",
	CategoryImageInput:   "图片参数无效：支持 HTTPS 链接、base64 图片或 BPS 附件 ID，清晰度为 auto、low、high 或 original。 / Invalid Basispoints image input.",
	CategoryImageLimit:   "整段会话的图片超过本地桥接容量，请压缩图片或压缩会话历史后重试。 / Conversation images exceed a local bridge limit.",
	CategoryToolChoice:   "Basispoints 渠道只支持 tool_choice 为 auto 或 none。 / Basispoints supports tool_choice auto or none only.",
	CategoryToolCatalog:  "工具声明无法经 Basispoints 转发：只支持带名称的 function / custom 客户端工具。 / Only named function/custom client tools are supported.",
	CategoryHistoryRef:   "Basispoints 需要完整对话历史，不支持 previous_response_id 或 item_reference。 / Basispoints needs the expanded history.",
	CategoryReasoning:    "Basispoints 不支持该推理设置。 / Unsupported reasoning configuration.",
	CategoryOutputFormat: "结构化输出参数无效。 / Invalid structured output parameters.",
	CategoryModel:        "请求缺少 model。 / The request has no model.",
	CategoryRequestJSON:  "请求体不是合法 JSON，或 input 既不是文本也不是 Responses 条目数组。 / Invalid request JSON.",
	CategoryRequestShape: "请求包含 Basispoints 渠道不支持的内容。 / The request contains unsupported content.",
}

// UserMessage renders a bilingual explanation for the client.
func UserMessage(err error) string {
	if err == nil {
		return ""
	}
	msg := userMessages[Category(err)]
	if msg == "" {
		msg = userMessages[CategoryRequestShape]
	}
	return msg + " (" + err.Error() + ")"
}

// ProtocolFailureMessage explains a failed stream to the client.
func ProtocolFailureMessage(err error) string {
	if err == nil {
		return ""
	}
	detail := err.Error()
	switch {
	case strings.Contains(detail, "unsupported native tool"), strings.Contains(detail, "outside the client's catalog"):
		return "模型尝试调用 Basispoints 渠道无法转发的内置工具，本次未执行任何工具，请重试。 / The model called a native tool that cannot be relayed; nothing was executed. (" + detail + ")"
	case strings.Contains(detail, "SSE"), strings.Contains(detail, "omitted"), strings.Contains(detail, "too many tool items"):
		return "Basispoints 上游返回的数据流不完整或格式异常，请重试。 / The upstream stream was incomplete. (" + detail + ")"
	default:
		return "模型返回的工具调用没有遵守传输格式约定，本次未执行任何工具，请重试。 / The tool call did not follow the transport contract; nothing was executed. (" + detail + ")"
	}
}

// IsNativeToolLeak reports whether err means the model called a tool that does
// not exist for this request (as opposed to misformatting a real one).
func IsNativeToolLeak(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "unsupported native tool") || strings.Contains(msg, "outside the client's catalog")
}
