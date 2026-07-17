// ======================================================================================================
// 📂 sre/claims.rs — Re-export Claims và TokenManager cho SRE domain
// ======================================================================================================

// Re-export để các module khác gọi qua domain path
#[allow(unused_imports)]
pub use crate::billing::claims::TokenManager as SharedTokenManager;
#[allow(unused_imports)]
pub use crate::user::claims::Claims as SreClaims;
