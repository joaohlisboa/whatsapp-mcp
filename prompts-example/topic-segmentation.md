Analyze these WhatsApp messages and segment them into distinct topics.
Each topic should represent a coherent discussion or specific theme.

**Instructions:**
- Identify discussions about companies, business, operations, scheduling, etc.
- Group related messages even if they're not sequential
- Use descriptive and concise topic names (2-6 words)
- If there's only one main topic, return it anyway

**Response format (JSON):**
```json
{
  "topic_name_1": {
    "messages": [0, 1, 4, 7],
    "summary": "Brief topic description"
  },
  "topic_name_2": {
    "messages": [2, 3, 5, 6],
    "summary": "Brief topic description"
  }
}
```

**Examples of typical topics:**
- company_evaluation_X
- meeting_scheduling
- operational_updates
- pipeline_discussion
- multiple_analysis

---

**Messages from {{DATE}}:**

{{MESSAGES}}