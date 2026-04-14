-- ============================================================
-- Migration 001: Extensions & Enum Types
-- ConvertChain Fintech Platform
-- PostgreSQL 16 · UTF-8 · timezone: UTC
-- ============================================================

-- Enable required PostgreSQL extensions
-- These add capabilities that PostgreSQL doesn't have by default
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";       -- Generates UUID v4 primary keys
CREATE EXTENSION IF NOT EXISTS "pgcrypto";         -- Cryptographic functions (hashing, encryption)
CREATE EXTENSION IF NOT EXISTS "pg_stat_statements"; -- Query performance monitoring

-- ─────────────────────────────────────────────
-- ENUM TYPES
-- Enums restrict a column to a fixed set of values.
-- Like a dropdown menu — only these exact options are allowed.
-- ─────────────────────────────────────────────

-- User registration/KYC status progression
CREATE TYPE user_status AS ENUM (
  'UNREGISTERED',      -- Just sent first message, no data collected yet
  'KYC_IN_PROGRESS',   -- Gave consent, currently submitting documents
  'KYC_PENDING',       -- Documents submitted, awaiting verification
  'KYC_APPROVED',      -- Verified — can trade
  'KYC_REJECTED'       -- Verification failed — can retry after 24h
);

-- KYC verification tier (determines transaction limits)
CREATE TYPE kyc_tier AS ENUM (
  'TIER_0',   -- Unverified: no transactions allowed
  'TIER_1',   -- BVN + NIN verified: $0–$5,000/month
  'TIER_2',   -- + selfie + proof of address: $5k–$20k/month
  'TIER_3',   -- + document verification: $20k–$100k/month
  'TIER_4'    -- Business KYC (CAC cert): $100k+/month
);

-- Trade lifecycle states (13 possible states)
CREATE TYPE trade_status AS ENUM (
  'QUOTE_PROVIDED',          -- Price quote shown to user
  'QUOTE_EXPIRED',           -- User didn't accept within 2 minutes
  'TRADE_CREATED',           -- User accepted quote, trade record created
  'PENDING_DEPOSIT',         -- Waiting for user to send crypto
  'DEPOSIT_RECEIVED',        -- Blockchain shows incoming transaction (unconfirmed)
  'DEPOSIT_CONFIRMED',       -- Blockchain confirmations met (2 for BTC)
  'CONVERSION_IN_PROGRESS',  -- Selling crypto on exchange (Binance/Bybit)
  'CONVERSION_COMPLETED',    -- Crypto sold, stablecoin received
  'PAYOUT_PENDING',          -- NGN payout initiated via Graph Finance
  'PAYOUT_COMPLETED',        -- Money landed in user's bank account
  'PAYOUT_FAILED',           -- Bank payout failed (retry or dispute)
  'DISPUTE',                 -- Manual intervention required
  'CANCELLED'                -- Trade cancelled (expired or user-initiated)
);

-- Communication channel the user came from
CREATE TYPE channel_type AS ENUM ('WHATSAPP', 'TELEGRAM');

-- Supported cryptocurrencies and fiat currencies
CREATE TYPE currency_code AS ENUM ('BTC', 'ETH', 'USDC', 'USDT', 'NGN', 'USD');

-- Types of KYC documents users can submit
CREATE TYPE kyc_doc_type AS ENUM (
  'NIN',              -- National Identification Number
  'BVN',              -- Bank Verification Number
  'PVC',              -- Permanent Voter's Card
  'PASSPORT',         -- International passport
  'DRIVERS_LICENSE',  -- Driver's license
  'PROOF_OF_ADDRESS', -- Utility bill, bank statement, etc.
  'SELFIE',           -- Live selfie for liveness check
  'CAC_CERT',         -- Corporate Affairs Commission certificate (business)
  'TIN',              -- Tax Identification Number
  'CAC_FORM'          -- CAC registration form (business)
);