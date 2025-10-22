#!/usr/bin/env node

/**
 * {{PROJECT_NAME}} - A Node.js application
 */

console.log('Hello from {{PROJECT_NAME}}!');
console.log('Node.js development environment is ready!');

// Example HTTP server (uncomment to use)
/*
const http = require('http');

const server = http.createServer((req, res) => {
  res.writeHead(200, { 'Content-Type': 'text/plain' });
  res.end('Hello from {{PROJECT_NAME}}!\n');
});

const PORT = process.env.PORT || 3000;
server.listen(PORT, '0.0.0.0', () => {
  console.log(`Server running at http://0.0.0.0:${PORT}/`);
});
*/