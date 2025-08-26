module.exports = {
  apps: [{
    name: 'market-service',
    script: './market-service',    // compiled Go binary
    instances: 1,
    autorestart: true,
    watch: false,
    max_memory_restart: '4G',
    env: {
      NODE_ENV: 'production',
      PORT: 8081,   // 👈 ensure Go service listens on 8081
      FORCE_REFRESH: 'false'  // Set to 'true' to force API refresh on startup
    },
    env_production: {
      NODE_ENV: 'production',
      PORT: 8081,
      FORCE_REFRESH: 'false'  // Ultra-fast startup using existing database
    },
    env_refresh: {
      NODE_ENV: 'production', 
      PORT: 8081,
      FORCE_REFRESH: 'true'   // Force fresh data fetch from API
    },
    out_file: './logs/market-service-out.log',
    error_file: './logs/market-service-error.log',
    log_date_format: 'YYYY-MM-DD HH:mm:ss Z',
    merge_logs: true,
    kill_timeout: 5000,
    listen_timeout: 10000,
    restart_delay: 1000,
    // WebSocket stability and monitoring configurations
    max_restarts: 10,
    min_uptime: '10s',
    // Health check configuration
    health_check_grace_period: 3000,
    // Additional environment variables for WebSocket stability
    env_websocket_debug: {
      NODE_ENV: 'production',
      PORT: 8081,
      FORCE_REFRESH: 'false',
      WEBSOCKET_DEBUG: 'true',
      HEARTBEAT_INTERVAL: '30000',
      PING_TIMEOUT: '90000',
      MAX_MISSED_PINGS: '3'
    }
  }]
};
