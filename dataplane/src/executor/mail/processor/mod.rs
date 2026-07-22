pub mod batcher;
pub mod jmap;
pub mod model;
pub mod stream;
pub mod template;

pub use batcher::MailBatcherHandle;
pub use jmap::JmapClient;
pub use model::SenderProfile;
pub use stream::MailMessageProcessor;
