use super::{render_html, render_subject};
use std::collections::HashMap;

#[test]
fn html_variables_are_escaped_but_subject_is_plain_text() {
    let variables = HashMap::from([("name".to_string(), "<Alice & Bob>".to_string())]);
    assert_eq!(
        render_html("<p>{{name}}</p>", &variables),
        "<p>&lt;Alice &amp; Bob&gt;</p>"
    );
    assert_eq!(
        render_subject("Hello {{name}}", &variables),
        "Hello <Alice & Bob>"
    );
}
