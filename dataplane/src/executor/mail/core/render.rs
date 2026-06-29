use std::collections::HashMap;

/// Render template bằng cách thay thế các placeholder dạng {{variable}}
pub fn render_template(template: &str, variables: &HashMap<String, String>) -> String {
    let mut rendered = template.to_string();
    for (key, val) in variables {
        let placeholder = format!("{{{{{}}}}}", key);
        rendered = rendered.replace(&placeholder, val);
    }
    rendered
}
