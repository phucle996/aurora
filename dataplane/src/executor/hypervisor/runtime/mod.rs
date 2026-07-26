mod provider_binding;

pub(super) use provider_binding::VmProviderBindingRuntime;

#[cfg(test)]
#[path = "../test/provider_binding.rs"]
mod provider_binding_tests;
