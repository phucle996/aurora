use super::*;

fn client() -> JmapClient {
    JmapClient {
        http: Client::new(),
        endpoint: "http://localhost/jmap".to_string(),
        auth: JmapAuth::Bearer("test".to_string()),
        sender: Arc::new(SenderProfile {
            id: "platform-default".to_string(),
            version: 1,
            from_address: "noreply@example.com".to_string(),
            account_id: "account-1".to_string(),
            identity_id: "identity-1".to_string(),
            mailbox_id: "drafts-1".to_string(),
        }),
        max_retries: 0,
    }
}

fn mail(job_id: &str) -> PreparedMail {
    PreparedMail {
        job_id: job_id.to_string(),
        recipient: format!("{job_id}@example.com"),
        subject: "Subject".to_string(),
        text_body: None,
        html_body: Some("<p>Hello</p>".to_string()),
        estimated_bytes: 1024,
    }
}

#[test]
fn builds_one_email_and_submission_object_per_job() {
    let payload = client().build_batch_request(&[mail("job-1"), mail("job-2")]);
    let calls = payload["methodCalls"].as_array().unwrap();
    assert_eq!(calls.len(), 2);
    assert_eq!(calls[0][1]["create"].as_object().unwrap().len(), 2);
    assert_eq!(calls[1][1]["create"].as_object().unwrap().len(), 2);
    assert_eq!(
        calls[1][1]["onSuccessDestroyEmail"]
            .as_array()
            .unwrap()
            .len(),
        2
    );
    assert_eq!(
        calls[0][1]["create"]["mail-job1"]["mailboxIds"]["drafts-1"],
        true
    );
}

#[test]
fn maps_partial_submission_results_back_to_jobs() {
    let mails = vec![mail("job-1"), mail("job-2")];
    let response = json!({
        "methodResponses": [[
            "EmailSubmission/set",
            {
                "created": {"submit-job1": {"id": "submission-1"}},
                "notCreated": {"submit-job2": {"type": "invalidRecipients"}}
            },
            "submit-mails"
        ]]
    });
    let results = client().parse_batch_response(&mails, &response);
    assert_eq!(results.len(), 2);
    assert!(results[0].is_ok());
    assert_eq!(
        results[1].as_ref().unwrap_err().code,
        "MAIL_JMAP_SUBMISSION_REJECTED"
    );
    assert!(!results[1].as_ref().unwrap_err().retryable);
}
