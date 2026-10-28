package ai

import "regexp"

var sensitiveInput = regexp.MustCompile(`(?i)(1[3-9][0-9]{9}|[a-f0-9]{32,64}|(?:验证码|verification\s*code|sms\s*code)[^0-9]{0,8}[0-9]{6})`)

// SensitiveInput rejects credential-like user content before storage or model
// calls. It deliberately does not match decimal order IDs.
func SensitiveInput(text string) bool { return sensitiveInput.MatchString(text) }
