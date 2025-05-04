const fs = require('fs');
const path = require('path');

// Read the markdown file
const markdownPath = path.join(__dirname, '../docs/2025-05-04/01.md');
const markdownContent = fs.readFileSync(markdownPath, 'utf8');

// Regular expression to extract file definitions
const fileRegex = /__File: ([^_]+)__\n```(?:[^\n]*)\n([\s\S]*?)```/g;

// Process each file match
let match;
while ((match = fileRegex.exec(markdownContent)) !== null) {
  const filePath = match[1].trim();
  const fileContent = match[2];
  
  // Create the directory structure if it doesn't exist
  const dirPath = path.dirname(filePath);
  if (dirPath !== '.' && !fs.existsSync(dirPath)) {
    fs.mkdirSync(dirPath, { recursive: true });
    console.log(`Created directory: ${dirPath}`);
  }
  
  // Write the file
  fs.writeFileSync(filePath, fileContent);
  console.log(`Created file: ${filePath}`);
}

console.log('File extraction complete!');
