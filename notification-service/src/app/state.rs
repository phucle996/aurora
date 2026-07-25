use crate::application::auth::ConnectAuthorizer;
use crate::timeline::activity::ActivityService;
use crate::timeline::inbox::InboxService;
use std::sync::Arc;

#[derive(Clone)]
pub struct AppState {
    pub authorizer: Arc<ConnectAuthorizer>,
    pub activities: Arc<ActivityService>,
    pub inbox: Arc<InboxService>,
}
