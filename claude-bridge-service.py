#!/usr/bin/env python3
"""
Claude Bridge Background Service
Runs as a macOS launchd daemon to provide WhatsApp <-> Claude Code integration
"""
import asyncio
import subprocess
import json
import time
import logging
import sys
import signal
import os
from typing import Optional
from pathlib import Path

try:
    import aiohttp
except ImportError:
    print("Error: aiohttp not installed. Run: pip3 install aiohttp")
    sys.exit(1)

class ClaudeBridgeService:
    def __init__(self):
        self.bridge_url = "http://localhost:8080"
        self.your_phone = os.getenv('YOUR_PHONE', '')
        self.allowed_tools = os.getenv('ALLOWED_TOOLS', '*')
        self.last_query_time = 0
        self.min_query_interval = 10  # Rate limiting
        self.running = True
        
        # Setup logging
        self.setup_logging()
        
        # Validate environment variables
        if not self.your_phone:
            self.logger.error("YOUR_PHONE environment variable not set")
            sys.exit(1)
            
        self.logger.info(f"Allowed tools configured: {self.allowed_tools}")
            
        # Setup signal handlers for graceful shutdown
        signal.signal(signal.SIGTERM, self.signal_handler)
        signal.signal(signal.SIGINT, self.signal_handler)
        
    def setup_logging(self):
        """Setup logging to file and stdout"""
        log_dir = Path.home() / "Library" / "Logs" / "ClaudeBridge"
        log_dir.mkdir(parents=True, exist_ok=True)
        log_file = log_dir / "claude-bridge.log"
        
        # Create formatter
        formatter = logging.Formatter(
            '%(asctime)s - %(levelname)s - %(message)s'
        )
        
        # Setup logger
        self.logger = logging.getLogger('ClaudeBridge')
        self.logger.setLevel(logging.INFO)
        
        # File handler
        file_handler = logging.FileHandler(log_file)
        file_handler.setFormatter(formatter)
        self.logger.addHandler(file_handler)
        
        # Console handler (for debugging)
        console_handler = logging.StreamHandler()
        console_handler.setFormatter(formatter)
        self.logger.addHandler(console_handler)
        
    def signal_handler(self, signum, frame):
        """Handle shutdown signals gracefully"""
        self.logger.info(f"Received signal {signum}, shutting down gracefully...")
        self.running = False
        
    async def wait_for_bridge(self, max_retries=30):
        """Wait for WhatsApp bridge to be ready"""
        for attempt in range(max_retries):
            try:
                async with aiohttp.ClientSession() as session:
                    async with session.get(f"{self.bridge_url}/api/claude/pending", timeout=5) as resp:
                        if resp.status == 200:
                            self.logger.info("✅ WhatsApp bridge is ready")
                            return True
            except Exception as e:
                if attempt == 0:
                    self.logger.info("⏳ Waiting for WhatsApp bridge to start...")
                elif attempt % 5 == 0:  # Log every 5 attempts
                    self.logger.info(f"Still waiting for bridge... (attempt {attempt+1}/{max_retries})")
                
            await asyncio.sleep(2)
            
        self.logger.error("❌ WhatsApp bridge failed to start within timeout")
        return False
        
    async def poll_for_messages(self):
        """Continuously poll for self-messages"""
        self.logger.info("🚀 Claude Bridge Service started")
        
        # Wait for bridge to be ready
        if not await self.wait_for_bridge():
            return
            
        consecutive_errors = 0
        max_consecutive_errors = 10
        
        while self.running:
            try:
                async with aiohttp.ClientSession() as session:
                    async with session.get(f"{self.bridge_url}/api/claude/pending", timeout=10) as resp:
                        if resp.status == 200:
                            data = await resp.json()
                            if data.get('has_pending'):
                                self.logger.info(f"📱 Processing message: {data['message'][:50]}...")
                                await self.process_claude_request(data['message'], data['timestamp'])
                            
                        consecutive_errors = 0  # Reset error counter
                        
            except asyncio.CancelledError:
                break
            except Exception as e:
                consecutive_errors += 1
                self.logger.error(f"Polling error ({consecutive_errors}/{max_consecutive_errors}): {e}")
                
                if consecutive_errors >= max_consecutive_errors:
                    self.logger.error("Too many consecutive errors, shutting down")
                    break
                    
                # Exponential backoff
                wait_time = min(30, 2 ** consecutive_errors)
                await asyncio.sleep(wait_time)
                continue
                
            await asyncio.sleep(2)  # Normal polling interval
            
        self.logger.info("🛑 Claude Bridge Service stopped")
    
    async def process_claude_request(self, message: str, timestamp: str):
        """Process a Claude request using host's Claude Code CLI"""
        
        # Rate limiting
        current_time = time.time()
        if current_time - self.last_query_time < self.min_query_interval:
            await self.send_response("⏰ Please wait 10 seconds between queries", timestamp)
            return
            
        self.last_query_time = current_time
        
        try:
            # Clean and limit input
            clean_message = message.strip()[:1000]
            
            self.logger.info(f"🤖 Calling Claude Code: {clean_message[:100]}...")
            
            # Use shell script to execute Claude in proper terminal context
            # This ensures MCP servers and authentication work properly
            # The shell script sources ~/.zshrc which provides all necessary
            # environment including MCP configurations
            script_path = Path(__file__).parent / 'claude-executor.sh'
            
            if not script_path.exists():
                self.logger.error(f"Shell script not found at {script_path}")
                await self.send_response("❌ Claude executor script not found", timestamp)
                return
                
            # Pass ALLOWED_TOOLS to the subprocess
            env = os.environ.copy()
            env['ALLOWED_TOOLS'] = self.allowed_tools
            
            result = await asyncio.create_subprocess_exec(
                '/bin/bash', str(script_path), clean_message,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
                env=env
            )
            
            stdout, stderr = await asyncio.wait_for(
                result.communicate(), 
                timeout=300.0  # 5 minutes timeout
            )
            
            if result.returncode == 0:
                response = stdout.decode('utf-8').strip()
                # Log the response content (truncated if too long)
                response_preview = response[:200] + "..." if len(response) > 200 else response
                self.logger.info(f"✅ Claude responded ({len(response)} chars): {response_preview}")
                await self.send_chunked_response(f"🤖 Claude:\n{response}", timestamp)
            else:
                error_msg = stderr.decode('utf-8').strip()
                self.logger.error(f"❌ Claude Code error: {error_msg}")
                await self.send_response(f"❌ Claude Error: {error_msg[:300]}", timestamp)
                
        except asyncio.TimeoutError:
            self.logger.warning("⏰ Claude Code timeout")
            await self.send_response("⏰ Claude Code timed out (set limit)", timestamp)
        except FileNotFoundError:
            self.logger.error("❌ Claude Code CLI not found")
            await self.send_response("❌ Claude Code CLI not found. Please install it first.", timestamp)
        except Exception as e:
            self.logger.error(f"❌ Unexpected error: {e}")
            await self.send_response(f"❌ Failed to call Claude: {str(e)[:200]}", timestamp)
    
    async def send_chunked_response(self, response: str, timestamp: str):
        """Split long responses into WhatsApp-friendly chunks"""
        max_length = 1500  # WhatsApp message limit
        
        if len(response) <= max_length:
            await self.send_response(response, timestamp)
            return
            
        # Split into chunks
        chunks = [response[i:i+max_length] for i in range(0, len(response), max_length)]
        
        self.logger.info(f"📤 Sending response in {len(chunks)} chunks")
        
        for i, chunk in enumerate(chunks):
            prefix = f"[{i+1}/{len(chunks)}] " if len(chunks) > 1 else ""
            chunk_preview = chunk[:100] + "..." if len(chunk) > 100 else chunk
            self.logger.info(f"📤 Sending chunk {i+1}/{len(chunks)}: {chunk_preview}")
            await self.send_response(f"{prefix}{chunk}", timestamp)
            if i < len(chunks) - 1:  # Don't sleep after last chunk
                await asyncio.sleep(1.5)  # Delay between chunks
    
    async def send_response(self, response: str, timestamp: str):
        """Send response back through container bridge"""
        try:
            async with aiohttp.ClientSession() as session:
                payload = {
                    "recipient": self.your_phone,
                    "message": response,
                    "timestamp": timestamp
                }
                async with session.post(f"{self.bridge_url}/api/claude/respond", json=payload, timeout=15) as resp:
                    if resp.status == 200:
                        # Log a preview of what was sent
                        message_preview = response[:100] + "..." if len(response) > 100 else response
                        self.logger.info(f"✅ Response sent: {message_preview}")
                    else:
                        self.logger.error(f"❌ Failed to send response: HTTP {resp.status}")
        except Exception as e:
            self.logger.error(f"❌ Error sending response: {e}")

async def main():
    service = ClaudeBridgeService()
    try:
        await service.poll_for_messages()
    except KeyboardInterrupt:
        service.logger.info("Interrupted by user")
    except Exception as e:
        service.logger.error(f"Fatal error: {e}")
        sys.exit(1)

if __name__ == "__main__":
    # Check if Claude CLI is available
    claude_path = '/opt/homebrew/bin/claude'
    try:
        if not Path(claude_path).exists():
            print(f"❌ Error: Claude CLI not found at {claude_path}")
            print("Please install Claude Code CLI first: https://docs.anthropic.com/en/docs/claude-code")
            sys.exit(1)
        else:
            print(f"✅ Claude CLI found at: {claude_path}")
    except Exception as e:
        print(f"❌ Error checking Claude CLI: {e}")
        sys.exit(1)
    
    # Run the service
    asyncio.run(main())