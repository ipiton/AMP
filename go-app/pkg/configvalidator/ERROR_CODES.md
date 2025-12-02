# Error Codes Reference

Complete reference for all validation error, warning, info, and suggestion codes.

---

## **Parser Errors** (E000-E009)

### E000: Generic Parse Error
**Severity**: Error
**Description**: Unknown parsing error occurred.
**Solution**: Check configuration syntax and format.

### E001: YAML Syntax Error
**Severity**: Error
**Description**: Invalid YAML syntax.
**Example**:
```yaml
route:
  receiver: default
  group_by: ['alertname'  # Missing closing bracket
```
**Solution**: Validate YAML syntax using a YAML linter.

### E002: JSON Syntax Error
**Severity**: Error
**Description**: Invalid JSON syntax.
**Solution**: Validate JSON using a JSON linter (e.g., `jq`).

### E003: File Too Large
**Severity**: Error
**Description**: Configuration file exceeds maximum size limit.
**Solution**: Split configuration into smaller files or increase `MaxFileSize` option.

### E004: Unknown Format
**Severity**: Error
**Description**: Unable to determine file format (not YAML or JSON).
**Solution**: Ensure file is valid YAML or JSON.

---

## **Structural Errors** (E010-E029)

### E010: Required Field Missing
**Severity**: Error
**Description**: A required field is not provided.
**Example**: Missing `name` field in receiver.
**Solution**: Add the required field to your configuration.

### E011: Invalid URL Format
**Severity**: Error
**Description**: URL is not properly formatted.
**Solution**: Ensure URL includes scheme (http/https) and valid hostname.

### E012: Invalid Email Address
**Severity**: Error
**Description**: Email address format is invalid.
**Solution**: Use format: `user@domain.com`

### E013: Value Below Minimum
**Severity**: Error
**Description**: Field value is below minimum allowed.
**Solution**: Increase value to meet minimum requirement.

### E014: Value Above Maximum
**Severity**: Error
**Description**: Field value exceeds maximum allowed.
**Solution**: Decrease value to meet maximum requirement.

### E015: Invalid Port Number
**Severity**: Error
**Description**: Port must be between 1 and 65535.
**Solution**: Provide a valid port number.

### E016: Value Must Be Positive
**Severity**: Error
**Description**: Field value must be greater than zero.
**Solution**: Set value to positive number.

### E017: Value Must Be Non-Negative
**Severity**: Error
**Description**: Field value must be zero or greater.
**Solution**: Set value to non-negative number.

### E018: Invalid Duration
**Severity**: Error
**Description**: Duration must be positive (e.g., '30s', '5m').
**Solution**: Provide valid duration string.

### E019: Invalid Regular Expression
**Severity**: Error
**Description**: Regex pattern syntax is invalid.
**Solution**: Check regex syntax for common issues (unmatched parentheses, invalid escapes).

### E020: Invalid Enum Value
**Severity**: Error
**Description**: Value must be one of allowed options.
**Solution**: Choose from allowed values listed in error message.

### E021: No Receivers Defined
**Severity**: Error
**Description**: At least one receiver is required.
**Solution**: Define at least one receiver in `receivers` section.

### E022: Receiver Name Required
**Severity**: Error
**Description**: Each receiver must have a unique name.
**Solution**: Provide a name for the receiver.

### E023: Duplicate Receiver Name
**Severity**: Error
**Description**: Receiver names must be unique.
**Solution**: Rename duplicate receiver to unique value.

### E025: Route Must Have Receiver
**Severity**: Error
**Description**: Route must specify receiver or have child routes.
**Solution**: Add `receiver` field or define `routes` (child routes).

### E026: Invalid Route Duration
**Severity**: Error
**Description**: Route duration fields must be positive.
**Solution**: Set `group_wait`, `group_interval`, `repeat_interval` to positive durations.

### E027: Inhibit Rule Source Match Required
**Severity**: Error
**Description**: Inhibit rule must specify source matchers.
**Solution**: Define `source_matchers` or `source_match`/`source_match_re`.

### E028: Inhibit Rule Target Match Required
**Severity**: Error
**Description**: Inhibit rule must specify target matchers.
**Solution**: Define `target_matchers` or `target_match`/`target_match_re`.

---

## **Route Errors** (E100-E109)

### E100: Root Route Required
**Severity**: Error
**Description**: Configuration must define a root route.
**Solution**: Add `route` section to configuration.

### E101: Route Tree Too Deep
**Severity**: Error
**Description**: Route nesting exceeds maximum depth (100).
**Solution**: Reduce route nesting depth.

### E102: Receiver Not Found
**Severity**: Error
**Description**: Route references non-existent receiver.
**Example**: Route receiver 'pagerduty-prod' not defined in receivers.
**Solution**: Add receiver to `receivers` section or fix receiver name typo.

### E103: Root Route Must Have Receiver
**Severity**: Error
**Description**: Root route must specify a receiver.
**Solution**: Add `receiver` field to root route.

### E104: Invalid Matcher Syntax
**Severity**: Error
**Description**: Matcher format is invalid.
**Solution**: Use format: `label=value`, `label!=value`, `label=~regex`, or `label!~regex`

### E105: Invalid Regex in Matcher
**Severity**: Error
**Description**: Regex pattern in matcher is invalid.
**Solution**: Check regex syntax and escaping.

### E106-E109: Label Name Validation
**Severity**: Error
**Description**: Invalid label name in matcher or group_by.
**Solution**: Label names must match `[a-zA-Z_][a-zA-Z0-9_]*`

---

## **Receiver Errors** (E110-E142)

### Webhook Errors (E113-E114)

#### E113: Webhook URL Required
**Severity**: Error
**Solution**: Provide valid webhook URL (e.g., 'http://example.com/webhook').

#### E114: Invalid Webhook URL
**Severity**: Error
**Solution**: Ensure URL is properly formatted with scheme and hostname.

### Slack Errors (E115-E117)

#### E115: Slack API URL Required
**Severity**: Error
**Solution**: Provide Slack webhook URL.

#### E116: Invalid Slack API URL
**Severity**: Error
**Solution**: Ensure Slack webhook URL is properly formatted.

#### E117: Slack Must Use HTTPS
**Severity**: Error
**Solution**: Slack webhooks must use HTTPS protocol.

### Email Errors (E118-E121)

#### E118: Email 'To' Required
**Severity**: Error
**Solution**: Provide recipient email address.

#### E119: Invalid Email Address
**Severity**: Error
**Solution**: Use valid email format (user@domain.com).

#### E120: Invalid 'From' Address
**Severity**: Error
**Solution**: Provide valid sender email address.

#### E121: Invalid Smarthost Format
**Severity**: Error
**Solution**: Use format: 'host:port' (e.g., 'smtp.gmail.com:587').

### PagerDuty Errors (E122-E125)

#### E122: PagerDuty Key Required
**Severity**: Error
**Solution**: Provide `routing_key` or deprecated `service_key`.

#### E123: Invalid PagerDuty URL
**Severity**: Error
**Solution**: Ensure PagerDuty URL is properly formatted.

#### E124: PagerDuty Must Use HTTPS
**Severity**: Error
**Solution**: PagerDuty requires HTTPS.

#### E125: Invalid PagerDuty Severity
**Severity**: Error
**Solution**: Severity must be: 'critical', 'error', 'warning', or 'info'.

### OpsGenie Errors (E126-E129)

#### E126: OpsGenie API Key Required
**Severity**: Error
**Solution**: Provide OpsGenie API key.

#### E127: Invalid OpsGenie URL
**Severity**: Error
**Solution**: Ensure OpsGenie API URL is properly formatted.

#### E128: OpsGenie Must Use HTTPS
**Severity**: Error
**Solution**: OpsGenie requires HTTPS.

#### E129: Invalid OpsGenie Priority
**Severity**: Error
**Solution**: Priority must be: 'P1', 'P2', 'P3', 'P4', or 'P5'.

### VictorOps Errors (E130-E134)

#### E130: VictorOps API Key Required
**Severity**: Error
**Solution**: Provide VictorOps API key.

#### E131: VictorOps Routing Key Required
**Severity**: Error
**Solution**: Provide routing key.

#### E132: Invalid VictorOps URL
**Severity**: Error
**Solution**: Ensure URL is properly formatted.

#### E133: VictorOps Must Use HTTPS
**Severity**: Error
**Solution**: VictorOps requires HTTPS.

#### E134: Invalid Message Type
**Severity**: Error
**Solution**: Message type must be: 'CRITICAL', 'WARNING', or 'INFO'.

### Pushover Errors (E135-E137)

#### E135: Pushover User Key Required
**Severity**: Error
**Solution**: Provide Pushover user key.

#### E136: Pushover Token Required
**Severity**: Error
**Solution**: Provide application token.

#### E137: Invalid Pushover Priority
**Severity**: Error
**Solution**: Priority must be: '-2', '-1', '0', '1', or '2'.

### WeChat Errors (E138-E141)

#### E138: WeChat API URL Required
**Severity**: Error
**Solution**: Provide WeChat API URL.

#### E139: Invalid WeChat URL
**Severity**: Error
**Solution**: Ensure URL is properly formatted.

#### E140: WeChat Must Use HTTPS
**Severity**: Error
**Solution**: WeChat requires HTTPS.

#### E141: WeChat Corp ID Required
**Severity**: Error
**Solution**: Provide corp_id.

### HTTP Config Error (E142)

#### E142: Invalid Proxy URL
**Severity**: Error
**Solution**: Ensure proxy URL is properly formatted.

---

## **Inhibition Errors** (E150-E154)

### E150: Source Matchers Required
**Severity**: Error
**Solution**: Define `source_matchers`.

### E151: Target Matchers Required
**Severity**: Error
**Solution**: Define `target_matchers`.

### E152: Invalid Label in 'Equal'
**Severity**: Error
**Solution**: Label names must match `[a-zA-Z_][a-zA-Z0-9_]*`.

### E153: Invalid Source Matcher
**Severity**: Error
**Solution**: Use format: `label=value`, `label!=value`, `label=~regex`, or `label!~regex`.

### E154: Invalid Target Matcher
**Severity**: Error
**Solution**: Check matcher syntax and regex pattern.

---

## **Global Config Errors** (E200-E209)

### E200: Invalid Resolve Timeout
**Severity**: Error
**Solution**: Set positive duration (e.g., '5m').

### E201: Invalid SMTP From Address
**Severity**: Error
**Solution**: Provide valid email address.

### E202: Invalid SMTP Smarthost
**Severity**: Error
**Solution**: Use format: 'host:port'.

### E203: Invalid Slack URL
**Severity**: Error
**Solution**: Ensure Slack webhook URL is properly formatted.

### E204: Global Slack Must Use HTTPS
**Severity**: Error
**Solution**: Slack requires HTTPS.

### E205: Invalid PagerDuty URL
**Severity**: Error
**Solution**: Ensure PagerDuty URL is properly formatted.

### E206: Global PagerDuty Must Use HTTPS
**Severity**: Error
**Solution**: PagerDuty requires HTTPS.

### E207: Invalid OpsGenie URL
**Severity**: Error
**Solution**: Ensure OpsGenie API URL is properly formatted.

### E208: Global OpsGenie Must Use HTTPS
**Severity**: Error
**Solution**: OpsGenie requires HTTPS.

### E209: Invalid HTTP Proxy URL
**Severity**: Error
**Solution**: Ensure proxy URL is properly formatted.

---

## **Warnings** (W000-W399)

### Deprecation Warnings (W100-W119)

#### W100: Deprecated 'match' Field
**Severity**: Warning
**Solution**: Migrate to `matchers` with format: `["label=value"]`.

#### W101: Deprecated 'match_re' Field
**Severity**: Warning
**Solution**: Migrate to `matchers` with regex operator: `["label=~regex"]`.

#### W116: Deprecated PagerDuty 'service_key'
**Severity**: Warning
**Solution**: Use `routing_key` for Events API v2.

#### W150-W153: Deprecated Inhibition Fields
**Severity**: Warning
**Solution**: Migrate to `source_matchers` and `target_matchers`.

### Security Warnings (W300-W311)

#### W300-W310: Hardcoded Secrets
**Severity**: Warning
**Description**: Secrets hardcoded in configuration.
**Solution**: Use `*_file` alternatives or environment variables.

#### W311: TLS Verification Disabled
**Severity**: Warning
**Description**: `insecure_skip_verify: true` detected.
**Solution**: Use proper CA certificates instead.

### Configuration Warnings (W111-W118, W200-W211)

#### W111: Insecure HTTP Protocol
**Severity**: Warning
**Solution**: Use HTTPS for secure communication.

#### W154: No 'Equal' Labels in Inhibit Rule
**Severity**: Warning
**Solution**: Add `equal` labels for more specific inhibition.

#### W200-W201: Unusual Resolve Timeout
**Severity**: Warning
**Solution**: Consider standard timeout ranges (1m-30m).

#### W202-W203: SMTP Configuration Issues
**Severity**: Warning
**Solution**: Complete SMTP authentication and enable TLS.

---

## **Info Messages** (I100-I399)

### I100: Root Route Has No group_by
**Severity**: Info
**Description**: Root route doesn't specify `group_by`.
**Impact**: Alerts will be grouped by default labels.

### I110: Slack Using Defaults
**Severity**: Info
**Description**: Slack config has no custom title/text.
**Suggestion**: Customize for better context.

### I200: No Global Config
**Severity**: Info
**Description**: No global configuration defined.
**Suggestion**: Consider adding global defaults.

### I300-I302: Security Summary
**Severity**: Info
**Description**: Summary of security findings.

---

## **Suggestions** (S100-S399)

### S100: Add group_by Labels
**Severity**: Suggestion
**Description**: Consider grouping alerts by common labels.
**Example**: `group_by: ['alertname', 'cluster']`

### S110: Email Missing 'From'
**Severity**: Suggestion
**Description**: Set `from` address for better deliverability.

### S111: Internal URL Detected
**Severity**: Suggestion
**Description**: URL points to localhost/internal address.
**Impact**: May not be accessible from all instances.

### S150: Broad Inhibition Rule
**Severity**: Suggestion
**Description**: Inhibition rule might be too broad.
**Solution**: Add more specific matchers or `equal` labels.

### S200: Missing SMTP From
**Severity**: Suggestion
**Description**: Consider setting global `smtp_from`.

### S300-S301: Security Best Practices
**Severity**: Suggestion
**Description**: Recommendations for improved security.

---

## **Error Code Categories**

| Range | Category | Count |
|-------|----------|-------|
| E000-E009 | Parser | 5 |
| E010-E099 | Structural | 19 |
| E100-E109 | Route | 10 |
| E110-E149 | Receiver | 33 |
| E150-E159 | Inhibition | 5 |
| E200-E209 | Global | 10 |
| **W000-W399** | **Warnings** | **60+** |
| **I000-I399** | **Info** | **10+** |
| **S000-S399** | **Suggestions** | **20+** |
| **TOTAL** | **All Codes** | **210+** |

---

## **Exit Codes**

| Code | Meaning | Strict | Lenient | Permissive |
|------|---------|--------|---------|------------|
| 0 | Success | ✅ | ✅ | ✅ |
| 1 | Errors present | ❌ | ❌ | ✅ |
| 2 | Warnings present | ❌ | ✅ | ✅ |

---

**For detailed examples and solutions, see [README.md](README.md)**
