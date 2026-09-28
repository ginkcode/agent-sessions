import { isBuiltin } from 'node:module';
import { existsSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

export async function resolve(specifier, context, nextResolve) {
  try {
    return await nextResolve(specifier, context);
  } catch (err) {
    if (!isBuiltin(specifier) && specifier.startsWith('.')) {
      const url = new URL(specifier, context.parentURL);
      const filePath = fileURLToPath(url);
      if (existsSync(filePath + '.ts')) {
        return nextResolve(specifier + '.ts', context);
      }
      if (existsSync(filePath + '.js')) {
        return nextResolve(specifier + '.js', context);
      }
      if (specifier.endsWith('.js') && existsSync(filePath.replace(/\.js$/, '.ts'))) {
        return nextResolve(specifier.replace(/\.js$/, '.ts'), context);
      }
    }
    throw err;
  }
}
