// basic.rs — sample Rust file exercising the symbols the extractor recognises:
// functions, structs, enums, traits, unions, impl blocks, constants, statics,
// type aliases, macro_rules!, derive, async/unsafe, visibility, #[cfg(test)].

use std::collections::HashMap;

// --- Constants & statics ---

pub const MAX_RETRIES: u32 = 5;
static GLOBAL_COUNTER: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);

// --- Type alias ---

pub type Result<T> = std::result::Result<T, AppError>;

// --- Enum ---

#[derive(Debug, Clone, PartialEq)]
pub enum AppError {
    NotFound(String),
    Unauthorized,
    Internal { message: String, code: u16 },
}

// --- Struct ---

#[derive(Debug, Clone)]
pub struct Config {
    pub host: String,
    pub port: u16,
    pub max_retries: u32,
}

// --- Union (rare but supported) ---

#[repr(C)]
pub union FloatOrInt {
    pub f: f32,
    pub i: i32,
}

// --- Trait ---

pub trait Repository {
    type Item;
    fn find_by_id(&self, id: u64) -> Option<Self::Item>;
    fn save(&mut self, item: Self::Item) -> Result<()>;
}

// --- Struct + impl block (inherent) ---

pub struct UserService {
    users: HashMap<u64, String>,
}

impl UserService {
    pub fn new() -> Self {
        Self {
            users: HashMap::new(),
        }
    }

    pub async fn get_user(&self, id: u64) -> Option<&String> {
        self.users.get(&id)
    }

    pub unsafe fn dangerous_operation(&mut self) {
        // Intentionally unsafe for testing.
    }
}

// --- Trait implementation ---

impl Repository for UserService {
    type Item = String;

    fn find_by_id(&self, id: u64) -> Option<Self::Item> {
        self.users.get(&id).cloned()
    }

    fn save(&mut self, item: Self::Item) -> Result<()> {
        self.users.insert(GLOBAL_COUNTER.load(std::sync::atomic::Ordering::SeqCst), item);
        Ok(())
    }
}

// --- Free functions ---

pub fn create_config(host: &str, port: u16) -> Config {
    Config {
        host: host.to_string(),
        port,
        max_retries: MAX_RETRIES,
    }
}

pub async fn run_server(config: Config) -> Result<()> {
    println!("Starting server on {}:{}", config.host, config.port);
    Ok(())
}

// --- macro_rules! ---

macro_rules! log_info {
    ($($arg:tt)*) => {
        println!("[INFO] {}", format!($($arg)*));
    };
}

// --- #[cfg(test)] block (should be skipped by extractor) ---

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_create_config() {
        let cfg = create_config("localhost", 8080);
        assert_eq!(cfg.port, 8080);
    }
}
