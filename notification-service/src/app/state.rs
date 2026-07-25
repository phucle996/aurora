use crate::application::auth::ConnectAuthorizer;
use std::sync::Arc;

#[derive(Clone)]
pub struct AppState {
    pub authorizer: Arc<ConnectAuthorizer>,
}
