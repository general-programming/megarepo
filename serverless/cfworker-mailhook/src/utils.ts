/**
 * Utility functions for the mailhook worker
 */
import { Address } from 'postal-mime';

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
