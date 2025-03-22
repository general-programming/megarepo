/**
 * Utility functions for the mailhook worker
 */
import { Address, Email, Attachment } from 'postal-mime';

/**
 * Creates an MD5 hash from a string
 * 
 * @param message - The string to hash
 * @returns MD5 hash as a hex string
 */
export async function md5(message: string): Promise<string> {
  // Convert the string to a Uint8Array
  const msgUint8 = new TextEncoder().encode(message);
  
  // Hash the message with MD5
  const hashBuffer = await crypto.subtle.digest('MD5', msgUint8);
  
  // Convert the ArrayBuffer to hex string
  const hashArray = Array.from(new Uint8Array(hashBuffer));
  const hashHex = hashArray.map(b => b.toString(16).padStart(2, '0')).join('');
  
  return hashHex;
}

/**
 * Formats an email address with name
 * 
 * @param address - The email address object
 * @returns Formatted email string
 */
export function formatEmail(address: Address): string {
  return `${address.name} <${address.address}>`
}

/**
 * Enriches email content by replacing CID references with base64 encoded images
 * and formatting the content for rendering
 * 
 * @param email - The parsed email object
 * @returns HTML content with embedded images
 */
export function toEnrich(email: Email): string {
  // Use HTML content if available, otherwise convert text to HTML
  let content = email.html || '';
  
  if (!content && email.text) {
    // Convert plain text to HTML by replacing newlines with <br> tags
    content = email.text
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/\n/g, '<br>');
    content = `<div style="font-family: monospace; white-space: pre-wrap;">${content}</div>`;
  }
  
  // If there's no content at all, return an empty message
  if (!content) {
    return '<div style="font-family: sans-serif; padding: 20px;">This email has no content.</div>';
  }
  
  // Create a map of Content-ID to attachment for quick lookup
  const cidMap = new Map<string, Attachment>();
  
  // Process attachments and build the CID map
  for (const attachment of email.attachments) {
    if (attachment.contentId) {
      // Remove angle brackets if present in Content-ID
      const contentId = attachment.contentId.replace(/[<>]/g, '');
      cidMap.set(contentId, attachment);
    }
  }
  
  // Replace CID references in HTML content with base64 encoded images
  if (cidMap.size > 0) {
    // Look for image tags with CID references
    content = content.replace(/src=["']cid:([^"']+)["']/gi, (match, cid) => {
      const attachment = cidMap.get(cid);
      
      if (attachment) {
        // Convert attachment content to base64
        let base64Content: string;
        
        if (typeof attachment.content === 'string') {
          // If content is already a string (possibly base64), use it directly
          base64Content = attachment.content;
          // If it's not already in base64 format, encode it
          if (attachment.encoding !== 'base64') {
            base64Content = btoa(base64Content);
          }
        } else {
          // Convert ArrayBuffer to base64
          const binary = Array.from(new Uint8Array(attachment.content as ArrayBuffer))
            .map(byte => String.fromCharCode(byte))
            .join('');
          base64Content = btoa(binary);
        }
        
        return `src="data:${attachment.mimeType};base64,${base64Content}"`;
      }
      
      // If attachment not found, return the original match
      return match;
    });
  }
  
  // Wrap the content in a styled container
  return content;
}
