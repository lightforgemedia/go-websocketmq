#!/usr/bin/env node

/**
 * This script parses a markdown document containing code files and extracts them
 * to their respective locations based on the file paths specified in the document.
 * 
 * Format expected in the document:
 * **File: path/to/file.ext**
 * ```language
 * // path/to/file.ext
 * file content...
 * ```
 */

const fs = require('fs');
const path = require('path');

// Read the document file
const docPath = process.argv[2] || 'docs/2025-05-07/01.md';

try {
  console.log(`Reading document: ${docPath}`);
  const content = fs.readFileSync(docPath, 'utf8');
  
  // Regular expression to find file sections
  // This pattern matches "**File: path/to/file**" and captures the file path
  const fileHeaderRegex = /\*\*File: ([^\*]+)\*\*/g;
  
  let match;
  let filesCreated = 0;
  
  while ((match = fileHeaderRegex.exec(content)) !== null) {
    const filePath = match[1].trim();
    console.log(`Found file: ${filePath}`);
    
    // Find the code block that follows this header
    const startPos = match.index + match[0].length;
    const codeBlockStart = content.indexOf('```', startPos);
    
    if (codeBlockStart === -1) {
      console.error(`No code block found for file: ${filePath}`);
      continue;
    }
    
    // Find the language identifier line end (e.g., ```go\n)
    const langLineEnd = content.indexOf('\n', codeBlockStart) + 1;
    
    // Find the end of the code block
    const codeBlockEnd = content.indexOf('```', langLineEnd);
    
    if (codeBlockEnd === -1) {
      console.error(`Code block not properly closed for file: ${filePath}`);
      continue;
    }
    
    // Extract the code content (including the first line with the file path comment)
    let codeContent = content.substring(langLineEnd, codeBlockEnd).trim();
    
    // Create directory if it doesn't exist
    const dirPath = path.dirname(filePath);
    try {
      fs.mkdirSync(dirPath, { recursive: true });
    } catch (err) {
      console.error(`Failed to create directory for ${filePath}: ${err.message}`);
      continue;
    }
    
    // Write the content to the file
    try {
      fs.writeFileSync(filePath, codeContent, 'utf8');
      console.log(`Created file: ${filePath}`);
      filesCreated++;
    } catch (err) {
      console.error(`Failed to write file ${filePath}: ${err.message}`);
    }
  }
  
  console.log(`Process completed. Created ${filesCreated} files.`);
  
} catch (err) {
  console.error(`Failed to read document: ${err.message}`);
  process.exit(1);
}
