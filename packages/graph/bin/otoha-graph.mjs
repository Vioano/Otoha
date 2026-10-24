#!/usr/bin/env node
// Otoha Graph owns the design interface; the bundled Archify CLI is its renderer.
import { runDesign } from '../design/cli.mjs';
import { runTeam } from '../team/cli.mjs';
if(process.argv[2]==='team')await runTeam(process.argv.slice(3));else await runDesign(process.argv.slice(2));
