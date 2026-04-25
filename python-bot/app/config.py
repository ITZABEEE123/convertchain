from pydantic import AnyHttpUrl, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_file=".env",
        env_file_encoding="utf-8",
        extra="ignore",
    )

    engine_url: AnyHttpUrl = "http://localhost:9000"
    service_token: str
    admin_api_token: str | None = None
    admin_telegram_user_ids: str = ""

    redis_url: str = "redis://localhost:6379/0"

    enabled_channels: str = "telegram"

    whatsapp_app_secret: str = ""
    whatsapp_verify_token: str = ""
    whatsapp_access_token: str = ""
    whatsapp_phone_number_id: str = ""

    telegram_bot_token: str
    telegram_webhook_secret: str | None = None
    telegram_trusted_delivery: bool = False

    telegram_provider: str = "direct"
    whatsapp_primary_provider: str = "meta"
    whatsapp_fallback_provider: str = "none"

    openclaw_base_url: AnyHttpUrl = "http://127.0.0.1:18789"
    openclaw_gateway_token: str | None = None
    openclaw_inbound_secret: str | None = None

    openclaw_telegram_enabled: bool = False
    openclaw_whatsapp_enabled: bool = False
    openclaw_whatsapp_mode: str = "fallback"
    openclaw_telegram_mode: str = "disabled"

    environment: str = "development"

    @property
    def is_production(self) -> bool:
        return self.environment == "production"

    @property
    def engine_url_str(self) -> str:
        return str(self.engine_url)

    @property
    def openclaw_base_url_str(self) -> str:
        return str(self.openclaw_base_url)

    @property
    def enabled_channel_set(self) -> set[str]:
        return {
            item.strip().lower()
            for item in self.enabled_channels.split(",")
            if item.strip()
        }

    @property
    def telegram_enabled(self) -> bool:
        return "telegram" in self.enabled_channel_set

    @property
    def whatsapp_enabled(self) -> bool:
        return "whatsapp" in self.enabled_channel_set

    @property
    def telegram_uses_openclaw(self) -> bool:
        return self.telegram_provider == "openclaw" or self.openclaw_telegram_enabled

    @property
    def whatsapp_openclaw_enabled(self) -> bool:
        return self.whatsapp_fallback_provider == "openclaw" or self.openclaw_whatsapp_enabled

    @property
    def openclaw_enabled(self) -> bool:
        return self.telegram_uses_openclaw or self.whatsapp_openclaw_enabled

    @property
    def telegram_webhook_secret_required(self) -> bool:
        return self.is_production and not self.telegram_trusted_delivery

    @property
    def admin_telegram_user_id_set(self) -> set[str]:
        return {
            item.strip()
            for item in self.admin_telegram_user_ids.split(",")
            if item.strip()
        }

    @field_validator("environment")
    @classmethod
    def validate_environment(cls, value: str) -> str:
        allowed = {"development", "staging", "production"}
        if value not in allowed:
            raise ValueError(f"environment must be one of {allowed}, got '{value}'")
        return value

    @field_validator("service_token")
    @classmethod
    def validate_service_token_not_empty(cls, value: str) -> str:
        if not value.strip():
            raise ValueError("SERVICE_TOKEN must not be empty")
        return value

    @field_validator("enabled_channels")
    @classmethod
    def validate_enabled_channels(cls, value: str) -> str:
        allowed = {"telegram", "whatsapp"}
        selected = {
            item.strip().lower()
            for item in value.split(",")
            if item.strip()
        }
        if not selected:
            raise ValueError("enabled_channels must include at least one channel")
        unsupported = selected - allowed
        if unsupported:
            raise ValueError(f"enabled_channels contains unsupported channels: {sorted(unsupported)}")
        return ",".join(sorted(selected))

    @field_validator("telegram_provider")
    @classmethod
    def validate_telegram_provider(cls, value: str) -> str:
        allowed = {"direct", "openclaw"}
        if value not in allowed:
            raise ValueError(f"telegram_provider must be one of {allowed}, got '{value}'")
        return value

    @field_validator("whatsapp_primary_provider")
    @classmethod
    def validate_whatsapp_primary_provider(cls, value: str) -> str:
        allowed = {"meta"}
        if value not in allowed:
            raise ValueError(f"whatsapp_primary_provider must be one of {allowed}, got '{value}'")
        return value

    @field_validator("whatsapp_fallback_provider")
    @classmethod
    def validate_whatsapp_fallback_provider(cls, value: str) -> str:
        allowed = {"none", "openclaw"}
        if value not in allowed:
            raise ValueError(f"whatsapp_fallback_provider must be one of {allowed}, got '{value}'")
        return value

    @field_validator("openclaw_whatsapp_mode", "openclaw_telegram_mode")
    @classmethod
    def validate_openclaw_modes(cls, value: str) -> str:
        allowed = {"primary", "fallback", "disabled"}
        if value not in allowed:
            raise ValueError(f"OpenClaw mode must be one of {allowed}, got '{value}'")
        return value


settings = Settings()
