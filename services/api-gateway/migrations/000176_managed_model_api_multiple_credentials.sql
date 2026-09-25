-- Each configuration owns its encrypted credential. A school may configure
-- the same provider and model more than once with different API keys.
DROP INDEX uq_managed_model_api_provider_model;
