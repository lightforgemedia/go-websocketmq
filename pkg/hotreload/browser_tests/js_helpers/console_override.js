// Store console logs
window.consoleLog = [];
const originalConsole = console.log;
console.log = function() {
    window.consoleLog.push(Array.from(arguments).join(' '));
    originalConsole.apply(console, arguments);
};