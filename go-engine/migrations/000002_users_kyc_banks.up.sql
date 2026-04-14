-- ============================================================
-- Migration 002: Users, KYC Documents & Bank Accounts
-- ============================================================

-- ─────────────────────────────────────────────
-- USERS TABLE
-- The central identity table. Every person who interacts
-- with the bot gets a record here.
-- ─────────────────────────────────────────────

CREATE TABLE users (
  -- Primary key: randomly generated UUID (not sequential)
  id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

  -- Which messaging platform the user came from
  channel_type      channel_type NOT NULL,

  -- Their unique ID on that platform
  -- WhatsApp: phone number like "2348012345678"
  -- Telegram: chat_id like "123456789"
  channel_user_id   VARCHAR(128) NOT NULL,

  -- Personal information (collected during KYC)
  phone_number      VARCHAR(20),
  email             VARCHAR(255),
  first_name        VARCHAR(100),
  last_name         VARCHAR(100),
  date_of_birth     DATE,

  -- Registration and verification status
  status            user_status NOT NULL DEFAULT 'UNREGISTERED',
  kyc_tier          kyc_tier NOT NULL DEFAULT 'TIER_0',

  -- Graph Finance person ID (created when KYC is approved)
  -- This links our user to Graph Finance's system for payouts
  graph_person_id   VARCHAR(64),

  -- Consent tracking (legal requirement)
  consent_given_at  TIMESTAMPTZ,    -- When they typed "YES"
  consent_ip        INET,           -- IP address at time of consent

  -- Soft delete: instead of deleting users, we deactivate them
  is_active         BOOLEAN NOT NULL DEFAULT TRUE,

  -- Timestamps
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  -- UNIQUE CONSTRAINT: One account per platform per user
  -- A user can have both a WhatsApp and Telegram account,
  -- but not two WhatsApp accounts with the same number
  CONSTRAINT uq_channel_user UNIQUE (channel_type, channel_user_id)
);

-- Indexes: speed up common queries
-- Think of these like a book's index — instead of reading every page
-- to find a phone number, PostgreSQL jumps directly to matching rows
CREATE INDEX idx_users_phone ON users(phone_number);
CREATE INDEX idx_users_status ON users(status);
CREATE INDEX idx_users_kyc_tier ON users(kyc_tier);

-- ─────────────────────────────────────────────
-- KYC DOCUMENTS
-- Tracks every identity document a user submits.
-- One user can have multiple documents (NIN, BVN, selfie, etc.)
-- ─────────────────────────────────────────────

CREATE TABLE kyc_documents (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

  -- Which user submitted this document
  user_id         UUID NOT NULL REFERENCES users(id),

  -- What type of document (NIN, BVN, PASSPORT, etc.)
  doc_type        kyc_doc_type NOT NULL,

  -- The document number (e.g., NIN: 12345678901)
  -- IMPORTANT: This is encrypted at the application level before storage
  -- The database sees only the encrypted ciphertext, never the real number
  document_number VARCHAR(64),

  -- URL to the uploaded document image/file (S3 or Google Cloud Storage)
  file_url        TEXT,

  -- Which verification provider checked this document
  provider        VARCHAR(32),    -- 'smile_id', 'sumsub', 'manual'
  provider_ref    VARCHAR(128),   -- Their reference/job ID for tracking

  -- Verification result
  verified        BOOLEAN,
  verified_at     TIMESTAMPTZ,
  rejected_reason TEXT,           -- Why it was rejected (if applicable)

  -- Some documents expire (e.g., passport)
  expires_at      DATE,

  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_kyc_docs_user ON kyc_documents(user_id);
CREATE INDEX idx_kyc_docs_type ON kyc_documents(doc_type);

-- ─────────────────────────────────────────────
-- BANK ACCOUNTS
-- Where users receive their NGN payouts.
-- A user can have multiple bank accounts but one is "primary."
-- ─────────────────────────────────────────────

CREATE TABLE bank_accounts (
  id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

  -- Which user owns this bank account
  user_id           UUID NOT NULL REFERENCES users(id),

  -- Nigerian bank identification
  bank_code         VARCHAR(10) NOT NULL,   -- CBN bank code, e.g., "058" for GTBank
  account_number    VARCHAR(20) NOT NULL,   -- 10-digit NUBAN account number
  account_name      VARCHAR(200) NOT NULL,  -- Name on the account (verified via NIP)
  bank_name         VARCHAR(100),           -- Human-readable: "GTBank", "Access Bank"

  -- Graph Finance payout destination ID
  -- Created when the bank account is registered with Graph Finance
  graph_dest_id     VARCHAR(64),

  -- Flags
  is_primary        BOOLEAN NOT NULL DEFAULT FALSE,   -- Default payout destination
  is_verified       BOOLEAN NOT NULL DEFAULT FALSE,   -- NIP name enquiry passed
  is_active         BOOLEAN NOT NULL DEFAULT TRUE,    -- Soft delete

  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_bank_accounts_user ON bank_accounts(user_id);