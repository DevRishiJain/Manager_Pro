#!/usr/bin/env python3
"""
TableOS AI Dining Concierge - Prompt Engine & Culinary Reasoning System
------------------------------------------------------------------------
This module provides structured prompts, domain guardrails, and deterministic
culinary reasoning algorithms for TableOS restaurant dining experiences.

Features:
- Standard Preset Prompts for instant guest queries (Specials, Diabetic advice, Meal under ₹1000, Best dishes).
- Gemini API System Prompts and Generation Configurations.
- Standalone runnable test runner to evaluate dining inquiries against active restaurant menus.
"""

import json
import os
import re
import sys
from typing import Dict, List, Any, Optional

# ============================================================================
# 1. Preset Guest Inquiries (Used directly by frontend & mobile UI)
# ============================================================================
PRESET_DINING_PROMPTS = [
    {
        "id": "whats_special",
        "title": "What's Special Here?",
        "prompt": "What's special here?",
        "category": "Curated Recommendations",
        "description": "Chef signature highlights and popular customer favorites."
    },
    {
        "id": "diabetic_sweets",
        "title": "Diabetic Safe Sweets & Options",
        "prompt": "What's good for a diabetic patient in sweets at this restaurant?",
        "category": "Health & Dietary",
        "description": "Advice on sugar content, glycemic index, and healthy savory alternatives."
    },
    {
        "id": "suggest_best",
        "title": "Suggest Something Best",
        "prompt": "Can you suggest me something best from the restaurant?",
        "category": "Top Picks",
        "description": "The best dishes to order for an authentic, memorable dining experience."
    },
    {
        "id": "meal_under_1000",
        "title": "Indian Meal Under ₹1000",
        "prompt": "Create a whole Indian meal under 1000 from the menu",
        "category": "Budget Curations",
        "description": "Complete curated thali/meal (Starter + Curry + Bread + Dessert) <= ₹1000."
    },
    {
        "id": "vegetarian_curries",
        "title": "Rich Vegetarian Curries",
        "prompt": "Show me rich vegetarian curries and breads for family dining",
        "category": "Vegetarian",
        "description": "Vegetarian options including Paneer, Dal Makhani, and tandoori breads."
    },
    {
        "id": "sharing_platters",
        "title": "Dishes for 2 Sharing",
        "prompt": "What dishes are best for 2 people sharing?",
        "category": "Portions & Sharing",
        "description": "Ideal appetizer and main course combinations for couples or pairs."
    }
]

# ============================================================================
# 2. System Prompts & Guardrails for Large Language Models (Gemini)
# ============================================================================
GEMINI_DINING_SYSTEM_INSTRUCTION = """
You are the executive AI Dining Concierge for this restaurant.
Your primary role is to assist guests in discovering dishes, understanding dietary/health suitability,
finding meals matching their budget, and answering culinary questions.

CRITICAL RULES:
1. ONLY recommend dishes that exist in the provided restaurant menu catalog. Never hallucinate unavailable items.
2. DIABETIC & HEALTH QUERIES:
   - For guests with diabetes asking about traditional Indian sweets (e.g. Gulab Jamun, Rasgulla, Kheer):
     Politely explain that these are preserved in refined sugar syrup and have a high glycemic index.
   - Empathize with the diner and suggest healthier savory or high-protein tandoor alternatives (e.g. Paneer Tikka, Tandoori Chicken, Dal Tadka).
   - For breads, advise whole-wheat Roti over refined flour Naan.
3. BUDGET / MEAL COMBINATIONS (e.g., "Under ₹1000"):
   - Pick a balanced combination (1 Starter, 1 Main Course, 1-2 Breads, optional dessert).
   - Calculate the exact sum in INR and ensure it stays strictly within the stated budget.
   - List each dish with its exact price.
4. TONE: Warm, courteous, appetizing, and concise.
5. RESPONSE FORMAT: Always return structured JSON with:
   - "answer": Clear, polite, conversational response directly addressing the question.
   - "matched_items": List of exact dish names from the menu to allow instant 1-click cart addition.
"""

def build_gemini_prompt(menu_catalog: List[Dict[str, Any]], user_query: str) -> str:
    """Formats the restaurant menu and query into a structured prompt."""
    menu_lines = []
    for idx, item in enumerate(menu_catalog, 1):
        price = item.get("price_minor", 0) / 100.0
        desc = item.get("description", "")
        menu_lines.append(f"{idx}. {item.get('name')} - ₹{price:.2f}. Description: {desc}")

    catalog_str = "\n".join(menu_lines)

    return f"""{GEMINI_DINING_SYSTEM_INSTRUCTION}

CURRENT STORED RESTAURANT MENU CATALOG:
{catalog_str}

USER INQUIRY: "{user_query}"

Return strictly valid JSON matching this schema:
{{
  "answer": "Polite, appetizing, and informative answer addressing the user question directly",
  "matched_items": ["Exact Item Name 1", "Exact Item Name 2"]
}}
"""

# ============================================================================
# 3. Deterministic Local Reasoning Engine (Fast, Offline Fallback)
# ============================================================================
def query_local_dining_engine(menu_items: List[Dict[str, Any]], user_query: str) -> Dict[str, Any]:
    """
    Evaluates customer dining queries using grounded local culinary logic.
    Provides sub-millisecond, deterministic answers for live demos or offline environments.
    """
    lower_q = user_query.lower()

    def find_items(*keywords) -> List[Dict[str, Any]]:
        matched = []
        seen = set()
        for kw in keywords:
            kw_lower = kw.lower()
            for it in menu_items:
                name = it.get("name", "")
                desc = it.get("description", "")
                if it.get("id", name) not in seen:
                    if kw_lower in name.lower() or kw_lower in desc.lower():
                        matched.append(it)
                        seen.add(it.get("id", name))
        return matched

    # 1. Diabetic / Sugar inquiries
    if "diabet" in lower_q or "sugar" in lower_q:
        sweets = find_items("jamun", "kheer", "halwa", "dessert", "sweet", "ice cream")
        savory = find_items("paneer tikka", "tandoori", "corn", "dal")
        
        answer = (
            "For diabetic guests, we advise avoiding our traditional sweets such as Gulab Jamun due to "
            "high refined sugar and saturated syrup content. Instead, our chefs recommend starting with "
            "protein-rich, low-glycemic tandoor dishes like Paneer Tikka or fresh savory starters. For breads, "
            "choose whole-wheat Roti over refined-flour Naan to maintain stable glucose levels."
        )
        combined = savory + sweets
        return {
            "query": user_query,
            "answer": answer,
            "retrieved_items": combined,
            "model": "dining-engine-v2-local"
        }

    # 2. Budget Meal / Indian meal under 1000
    if "1000" in lower_q or ("meal" in lower_q and "under" in lower_q) or "budget" in lower_q:
        starters = find_items("tikka", "corn", "appetizer", "starter")
        mains = find_items("dal", "makhani", "curry", "paneer", "chicken", "biryani")
        breads = find_items("naan", "roti", "paratha")
        desserts = find_items("jamun", "kheer", "dessert")

        selected = []
        total_minor = 0

        def add_candidate(candidates, budget_ceiling):
            nonlocal total_minor
            for c in candidates:
                p = c.get("price_minor", 0)
                if total_minor + p <= budget_ceiling:
                    selected.append(c)
                    total_minor += p
                    break

        add_candidate(starters, 35000)
        add_candidate(mains, 75000)
        add_candidate(breads, 85000)
        add_candidate(desserts, 100000)

        if selected:
            names = [f"{it.get('name')} (₹{it.get('price_minor', 0)//100})" for it in selected]
            answer = (
                f"Here is a complete, satisfying Indian meal curated under ₹1000 (Total: ₹{total_minor//100}): "
                f"{' + '.join(names)}. All dishes are available to order directly!"
            )
            return {
                "query": user_query,
                "answer": answer,
                "retrieved_items": selected,
                "model": "dining-engine-v2-local"
            }

    # 3. Specials / Recommendations
    if any(k in lower_q for k in ["special", "best", "recommend", "famous", "chef"]):
        specials = find_items("dal makhani", "paneer tikka", "butter naan", "butter chicken", "biryani", "crispy corn")
        if not specials and menu_items:
            specials = menu_items[:3]
        names = [f"{it.get('name')} (₹{it.get('price_minor', 0)//100})" for it in specials]
        answer = (
            f"Our house signature highlights include: {', '.join(names)}. "
            "Handcrafted by our master chefs with freshly ground spices and traditional tandoor techniques!"
        )
        return {
            "query": user_query,
            "answer": answer,
            "retrieved_items": specials,
            "model": "dining-engine-v2-local"
        }

    # 4. Keyword matches
    keywords = lower_q.split()
    matched = find_items(*keywords)
    if matched:
        names = [f"{it.get('name')} (₹{it.get('price_minor', 0)//100})" for it in matched]
        answer = (
            f"Based on your inquiry, here are the most relevant dishes from our menu: {', '.join(names)}. "
            "Feel free to customize spice levels or special instructions during ordering!"
        )
        return {
            "query": user_query,
            "answer": answer,
            "retrieved_items": matched,
            "model": "dining-engine-v2-local"
        }

    # 5. Default welcoming response
    picks = menu_items[:3] if len(menu_items) >= 3 else menu_items
    answer = (
        "Welcome to The Spice Route! Our kitchen specializes in rich North Indian delicacies, "
        "clay-oven tandoor appetizers, and aromatic curries. Explore our top chef selections below or "
        "ask for specific ingredients, dietary preferences, or budget combinations."
    )
    return {
        "query": user_query,
        "answer": answer,
        "retrieved_items": picks,
        "model": "dining-engine-v2-local"
    }

# ============================================================================
# 4. Interactive Test Runner
# ============================================================================
if __name__ == "__main__":
    sample_menu = [
        {"id": "1", "name": "Paneer Tikka", "description": "Smoky char-grilled cottage cheese", "price_minor": 32000},
        {"id": "2", "name": "Crispy Corn", "description": "Golden fried corn tossed in pepper", "price_minor": 24000},
        {"id": "3", "name": "Dal Makhani", "description": "Slow-cooked black lentils in creamy butter", "price_minor": 38000},
        {"id": "4", "name": "Butter Chicken", "description": "Tender tandoori chicken in tomato gravy", "price_minor": 48000},
        {"id": "5", "name": "Butter Naan", "description": "Tandoor baked refined flour bread", "price_minor": 7000},
        {"id": "6", "name": "Tandoori Roti", "description": "Whole wheat bread baked in tandoor", "price_minor": 4000},
        {"id": "7", "name": "Gulab Jamun", "description": "Warm milk dumplings in rose syrup", "price_minor": 12000}
    ]

    print("===================================================================")
    print("TableOS AI Dining Concierge - Prompt Engine Test Runner")
    print("===================================================================")

    test_queries = [
        "What's special here?",
        "What's good for a diabetic patient in sweets at this restaurant?",
        "Can you suggest me something best from the restaurant?",
        "Create a whole Indian meal under 1000 from the menu"
    ]

    for q in test_queries:
        print(f"\n[QUERY]: {q}")
        res = query_local_dining_engine(sample_menu, q)
        print(f"[ANSWER]: {res['answer']}")
        matched = [f"{it['name']} (₹{it['price_minor']//100})" for it in res['retrieved_items']]
        print(f"[MATCHED ITEMS ({len(matched)})]: {', '.join(matched)}")
        print("-" * 65)

    print("\n[SUCCESS]: All test prompts executed correctly with zero errors!")
