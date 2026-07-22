use super::*;
use crate::executor::mail::runtime::configuration::compile_template_tokens;

fn template(subject: &str, html: &str) -> RuntimeTemplateSnapshot {
    RuntimeTemplateSnapshot {
        template_id: uuid::Uuid::nil().to_string(),
        template_revision: 1,
        template_version: 1,
        content_sha256: [1; 32],
        subject_template: subject.to_string(),
        html_template: html.to_string(),
        subject_tokens: compile_template_tokens(subject).unwrap_or_default(),
        html_tokens: compile_template_tokens(html).unwrap_or_default(),
    }
}

#[test]
fn fixed_envelope_normalizes_scalar_parameters() {
    let envelope = decode_fixed_envelope(
        br#"{"to":"alice@example.com","parameter":{"name":"Alice","amount":123,"paid":true}}"#,
    )
    .expect("valid envelope");
    assert_eq!(envelope.to, "alice@example.com");
    assert_eq!(envelope.parameter["amount"], "123");
    assert_eq!(envelope.parameter["paid"], "true");
    assert_eq!(envelope.not_after_unix_ms, None);
}

#[test]
fn fixed_envelope_accepts_optional_expiry_and_rejects_duplicate_expiry() {
    let envelope = decode_fixed_envelope(
        br#"{"to":"alice@example.com","parameter":{},"not_after_unix_ms":4102444800000}"#,
    )
    .expect("valid envelope with expiry");
    assert_eq!(envelope.not_after_unix_ms, Some(4_102_444_800_000));

    assert!(decode_fixed_envelope(
        br#"{"to":"alice@example.com","parameter":{},"not_after_unix_ms":1,"not_after_unix_ms":2}"#,
    )
    .is_err());
}

#[test]
fn fixed_envelope_rejects_legacy_shape_nested_values_and_duplicates() {
    for payload in [
        br#"{"recipient":"alice@example.com","data":{}}"#.as_slice(),
        br#"{"to":"alice@example.com","parameter":{"nested":{"x":1}}}"#.as_slice(),
        br#"{"to":"alice@example.com","to":"bob@example.com","parameter":{}}"#.as_slice(),
        br#"{"to":"alice@example.com","parameter":{"name":"a","name":"b"}}"#.as_slice(),
    ] {
        assert!(decode_fixed_envelope(payload).is_err());
    }
}

#[test]
fn renderer_requires_exact_parameters_and_escapes_html() {
    let parameters = HashMap::from([
        ("name".to_string(), "<Alice & Bob>".to_string()),
        ("amount".to_string(), "123".to_string()),
    ]);
    let (subject, html) = render_template(
        &template("Hello {{ name }}", "<p>{{name}}: {{amount}}</p>"),
        &parameters,
        1024,
    )
    .expect("render");
    assert_eq!(subject, "Hello <Alice & Bob>");
    assert_eq!(html, "<p>&lt;Alice &amp; Bob&gt;: 123</p>");

    let mut extra = parameters.clone();
    extra.insert("unused".to_string(), "value".to_string());
    assert_eq!(
        render_template(&template("{{name}}", "{{amount}}"), &extra, 1024),
        Err("MAIL_PARAMETER_UNKNOWN")
    );
    assert_eq!(
        render_template(&template("{{name}}", "{{missing}}"), &parameters, 1024),
        Err("MAIL_PARAMETER_REQUIRED")
    );
}

#[test]
fn renderer_rejects_header_injection_and_unclosed_tokens() {
    let parameters = HashMap::from([("name".to_string(), "Alice\r\nBcc: x@y.test".to_string())]);
    assert_eq!(
        render_template(
            &template("Hello {{name}}", "<p>{{name}}</p>"),
            &parameters,
            1024
        ),
        Err("MAIL_TEMPLATE_SUBJECT_INVALID")
    );
    assert_eq!(
        compile_template_tokens("Hello {{name"),
        Err("MAIL_TEMPLATE_SYNTAX_INVALID")
    );
}
