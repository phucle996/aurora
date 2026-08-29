use crate::service::{ActivityService, ConnectAuthorizer, NotificationService};
use std::sync::Arc;

#[derive(Clone)]
pub struct AppState {
    pub authorizer: Arc<ConnectAuthorizer>,
    pub activities: Arc<ActivityService>,
    pub inbox: Arc<NotificationService>,
}
