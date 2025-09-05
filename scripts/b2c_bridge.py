#!/usr/bin/env python
"""
Python B2C Bridge for Golang Market Service
Handles B2C login, subscription, and streams market data to Golang via stdout
"""

import sys
import json
import time
import logging
import asyncio
from typing import Dict, List
import pyotp
from dotenv import load_dotenv
import os

# Load environment variables
load_dotenv()

# Configure logging to stderr (stdout is used for data)
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s',
    stream=sys.stderr
)
logger = logging.getLogger(__name__)

# Import B2C WebSocket client
# Add the b2c-api-python directory to Python path
script_dir = os.path.dirname(os.path.abspath(__file__))
project_root = os.path.dirname(script_dir)  # Go up one level from scripts/ to project root
b2c_api_path = os.path.join(project_root, 'b2c-api-python')

# Debug: Print paths to understand the issue
logger.info(f"🔍 Script directory: {script_dir}")
logger.info(f"🔍 Project root: {project_root}")
logger.info(f"🔍 B2C API path: {b2c_api_path}")
logger.info(f"🔍 Path exists: {os.path.exists(b2c_api_path)}")

# Try multiple possible paths for b2c-api-python
possible_paths = [
    b2c_api_path,  # Original calculated path
    os.path.join(os.getcwd(), 'b2c-api-python'),  # Current working directory
    '/home/rohitt/odin-streamer/b2c-api-python',  # Absolute path
]

b2c_found = False
for path in possible_paths:
    if os.path.exists(path):
        logger.info(f"✅ Found b2c-api-python at: {path}")
        sys.path.insert(0, path)  # Insert at beginning for priority
        b2c_found = True
        break
    else:
        logger.info(f"❌ Path not found: {path}")

if not b2c_found:
    logger.error("❌ Could not find b2c-api-python directory in any expected location")

try:
    from pycloudrestapi import IBTConnect
except ImportError as e:
    logger.error(f"❌ Failed to import pycloudrestapi: {e}")
    logger.error(f"❌ Searched in: {b2c_api_path}")
    logger.error("❌ Make sure b2c-api-python directory exists in project root")
    sys.exit(1)

class B2CBridge:
    """Bridge between B2C WebSocket and Golang service"""
    
    def __init__(self, config: Dict):
        self.config = config
        self.client = None
        self.is_connected = False
        self.subscribed_tokens = set()
        
    def connect(self) -> bool:
        """Connect to B2C WebSocket"""
        try:
            logger.info("🔌 Connecting to B2C WebSocket...")
            
            # Initialize B2C client
            self.client = IBTConnect(params={
                "baseurl": self.config.get('api_url', ''),
                "api_key": self.config.get('api_key', ''),
                "debug": False
            })

            # Generate TOTP for login
            totp_secret = self.config.get('totp_secret', '')
            totp = pyotp.TOTP(totp_secret).now()
            
            logger.info(f"🔐 Generated TOTP: {totp}")
            
            # Login
            login_response = self.client.login(params={
                "userId": self.config.get('user_id', ''),
                "password": self.config.get('password', ''),
                "totp": totp
            })
            
            logger.info(f"🔐 Login response: {login_response.get('status', 'Unknown')}")
            
            if login_response.get("data") is not None:
                logger.info("✅ B2C Login successful")
                self.is_connected = True
                
                # Set up callbacks
                self.client.on_open_broadcast_socket = self._on_websocket_open
                self.client.on_close_broadcast_socket = self._on_websocket_close
                self.client.on_error_broadcast_socket = self._on_websocket_error
                self.client.on_touchline = self._on_market_data
                self.client.on_bestfive = self._on_bestfive
                
                return True
            else:
                logger.error(f"❌ B2C Login failed: {login_response}")
                return False
                
        except Exception as e:
            logger.error(f"❌ B2C Connection error: {e}")
            return False
    
    async def _on_websocket_open(self, message):
        """WebSocket open callback - subscribe to tokens with correct market segments"""
        logger.info("✅ B2C WebSocket connected")
        
        try:
            # Get token:exchange pairs from command line arguments
            token_exchange_pairs = sys.argv[1:] if len(sys.argv) > 1 else []
            
            if not token_exchange_pairs:
                logger.warning("⚠️ No tokens provided for subscription")
                return
            
            logger.info(f"🎯 Subscribing to {len(token_exchange_pairs)} tokens with exchange-specific market segments")
            
            # Parse token:exchange pairs and create instruments with correct market segments
            instruments = []
            for pair in token_exchange_pairs:
                if ':' in pair:
                    # Format: token:exchange (e.g., "476:NSE" or "500410:BSE")
                    token, exchange = pair.split(':', 1)
                    market_segment = "1" if exchange.upper() == "NSE" else "3"  # NSE=1, BSE=3
                    instruments.append({"MktSegId": market_segment, "token": token})
                    logger.info(f"📊 Token {token} -> {exchange} exchange -> Market Segment {market_segment}")
                else:
                    # Fallback: assume NSE if no exchange specified
                    token = pair
                    market_segment = "1"  # Default to NSE
                    instruments.append({"MktSegId": market_segment, "token": token})
                    logger.info(f"📊 Token {token} -> NSE (default) -> Market Segment {market_segment}")
            
            # Subscribe to tokens in optimal batches (based on B2C API example)
            batch_size = 50  # Smaller batch size for better B2C API compatibility
            total_batches = (len(instruments) + batch_size - 1) // batch_size
            
            logger.info(f"🚀 OPTIMIZED: Processing {len(instruments)} tokens in {total_batches} batches of {batch_size}")
            
            for i in range(0, len(instruments), batch_size):
                batch = instruments[i:i + batch_size]
                
                try:
                    await self.client.touchline_subscription(batch)
                    batch_num = i // batch_size + 1
                    logger.info(f"✅ Subscribed batch {batch_num}/{total_batches}: {len(batch)} tokens with correct market segments")
                    
                    # Add to subscribed set
                    for instrument in batch:
                        self.subscribed_tokens.add(instrument["token"])
                        
                    # Minimal delay for API stability
                    if batch_num < total_batches:  # No delay after last batch
                        await asyncio.sleep(0.02)  # Reduced to 20ms for faster processing
                    
                except Exception as e:
                    logger.error(f"❌ Error subscribing to batch {batch_num}: {e}")
                    continue
                    
            logger.info(f"🎯 Subscribed to {len(self.subscribed_tokens)} tokens successfully with exchange-aware market segments")
                
        except Exception as e:
            logger.error(f"❌ Error in websocket open callback: {e}")
    
    async def _on_websocket_close(self, close_msg):
        """WebSocket close callback"""
        logger.warning(f"⚠️ B2C WebSocket disconnected: {close_msg}")
        self.is_connected = False
        
    async def _on_websocket_error(self, error):
        """WebSocket error callback"""
        logger.error(f"❌ B2C WebSocket error: {error}")
        
    async def _on_market_data(self, message):
        """Market data callback - stream to Golang via stdout"""
        try:
            # Validate message structure
            if not isinstance(message, dict) or 'data' not in message:
                return
                
            stock_data = message['data']
            
            if not isinstance(stock_data, dict) or 'Scrip' not in stock_data:
                return
                
            if 'token' not in stock_data['Scrip']:
                return
                
            token_str = str(stock_data['Scrip']['token'])
            
            if not token_str or token_str in ['', 'None', '0']:
                return
            
            # Extract market data
            try:
                ltp = float(stock_data.get('LTP', '0').replace(',', ''))
                high = float(stock_data.get('HighPrice', '0').replace(',', ''))
                low = float(stock_data.get('LowPrice', '0').replace(',', ''))
                open_price = float(stock_data.get('OpenPrice', '0').replace(',', ''))
                close_price = float(stock_data.get('ClosePrice', '0').replace(',', ''))
                volume = int(float(stock_data.get('Volume', '0').replace(',', '')))
                percent_change = float(stock_data.get('PercNetChange', '0'))
                
                # Extract 52-week high/low data from B2C API
                week_52_high = float(stock_data.get('LifeTimeHigh', '0').replace(',', ''))
                week_52_low = float(stock_data.get('LifeTimeLow', '0').replace(',', ''))
                
                # Extract timestamp from B2C API (LUT = Last Update Time)
                b2c_timestamp = stock_data.get('LUT', '')
                if b2c_timestamp:
                    # Convert B2C timestamp to milliseconds if needed
                    # B2C LUT format is typically in seconds, convert to milliseconds
                    try:
                        timestamp_ms = int(float(b2c_timestamp) * 1000)
                    except (ValueError, TypeError):
                        timestamp_ms = int(time.time() * 1000)  # Fallback to current time
                else:
                    timestamp_ms = int(time.time() * 1000)  # Fallback to current time
                    
            except (ValueError, TypeError, AttributeError):
                return
            
            # Basic validation
            if ltp <= 0:
                return
            
            # Create market data JSON for Golang
            market_data = {
                'symbol': '',  # Will be filled by Golang using token mapping
                'token': token_str,
                'ltp': ltp,
                'high': high,
                'low': low,
                'open': open_price,
                'close': close_price,
                'volume': volume,
                'change': percent_change,
                'week_52_high': week_52_high,  # 52-week high from B2C LifeTimeHigh
                'week_52_low': week_52_low,    # 52-week low from B2C LifeTimeLow
                'prev_close': close_price,
                'avg_volume_5d': volume,       # Use current volume as estimate
                'timestamp': timestamp_ms      # Use B2C timestamp instead of current time
            }
            
            # Send to Golang via stdout (JSON per line)
            print(json.dumps(market_data), flush=True)
                    
        except Exception as e:
            logger.error(f"❌ Error processing market data: {e}")
            
    async def _on_bestfive(self, message):
        """Best 5 callback - not used but required"""
        pass
    
    async def start_websocket(self):
        """Start WebSocket connections"""
        try:
            logger.info("🔌 Starting B2C WebSocket connections...")
            
            # Run both connections concurrently
            await asyncio.gather(
                self.client.connect_broadcast_socket(),
                self.client.connect_message_socket()
            )
            
        except Exception as e:
            logger.error(f"❌ WebSocket error: {e}")
            self.is_connected = False

def load_b2c_config():
    """Load B2C configuration"""
    config_paths = [
        "configs/cloud_config.json",  # New centralized config location
        "config/cloud_config.json",
        "cloud_config.json",
        "../configs/cloud_config.json",
        "../config/cloud_config.json",
        "../cloud_config.json",
        "../../cloud_config.json",
        "../../trading-strategy-poc-V2/trading-strategy-poc/cloud_config.json",
        "../../../trading-strategy-poc-V2/trading-strategy-poc/cloud_config.json",
    ]
    
    for path in config_paths:
        try:
            with open(path, 'r') as f:
                config = json.load(f)
                logger.info(f"✅ Loaded B2C config from: {path}")
                return config
        except FileNotFoundError:
            continue
    
    logger.error("❌ Could not find cloud_config.json")
    return {}

async def main():
    """Main function"""
    try:
        logger.info("🐍 Starting Python B2C Bridge")
        
        # Load B2C configuration
        config = load_b2c_config()
        if not config:
            logger.error("❌ No B2C configuration found")
            sys.exit(1)
        
        # Create and connect bridge
        bridge = B2CBridge(config)
        if not bridge.connect():
            logger.error("❌ Failed to connect to B2C")
            sys.exit(1)
        
        # Start WebSocket connections
        await bridge.start_websocket()
        
    except KeyboardInterrupt:
        logger.info("🛑 Received shutdown signal")
    except Exception as e:
        logger.error(f"❌ Bridge error: {e}")
        sys.exit(1)

if __name__ == "__main__":
    # Check if tokens are provided
    if len(sys.argv) < 2:
        logger.error("❌ Usage: python b2c_bridge.py <token1> <token2> ...")
        sys.exit(1)
    
    logger.info(f"🎯 Bridge starting with {len(sys.argv) - 1} tokens")
    
    # Run the bridge
    asyncio.run(main())
